package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// postImages issues a POST to path (either "/api/images/remove" or
// "/api/images/prune") with body as the raw JSON string. confirm, when
// non-empty, is sent as X-Gantry-Confirm -- callers that want to test
// the guardrail's own rejection pass "" and check the response instead
// of setting a real value.
func postImages(t *testing.T, url, body, confirm string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if confirm != "" {
		req.Header.Set("X-Gantry-Confirm", confirm)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// eventCall records one Options.AppendEvent invocation's Event, for
// asserting image.removed is logged for exactly the successful
// deletions, never the failed ones.
type eventCall struct {
	Kind, Entity, Severity, Detail string
}

func capturingAppendEvent(calls *[]eventCall) func(store.Event) (int64, error) {
	return func(e store.Event) (int64, error) {
		*calls = append(*calls, eventCall{e.Kind, e.Entity, e.Severity, e.Detail})
		return int64(len(*calls)), nil
	}
}

func TestImagesGetDefaultsToEmptyWhenOptionsNil(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/images")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var dto ImagesDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Empty(t, dto.Images)
	require.Zero(t, dto.Summary.InUse)
	require.NotEmpty(t, dto.Summary.Note, "the upper-bound caveat must be present even in the nil-Options default")
}

func TestImagesGetShortensIDAndDefaultsRepoTagsForDangling(t *testing.T) {
	full := "sha256:" + fmt.Sprintf("%064x", 1)
	s := New(Options{Version: "test-1", Started: time.Now(), Images: func(context.Context) (ImagesDTO, error) {
		return ImagesDTO{Images: []ImageInfo{{ID: full, SizeBytes: 100, State: "dangling"}}}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/images")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var dto ImagesDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Len(t, dto.Images, 1)
	require.Len(t, dto.Images[0].ID, 12, "GET must show docker's own 12-char short id, not the full digest")
	require.Equal(t, full[7:19], dto.Images[0].ID)
	require.Equal(t, []string{"<none>"}, dto.Images[0].RepoTags)
}

// TestImagesGetShowsDigestRefInsteadOfNoneWhenTagsEmptyButDigestsArent
// pins F2's display half: a digest-pinned image (no RepoTags) is not
// "<none>" the way a truly dangling image is -- it must show its own
// digest reference instead, truncated the same 12-char way a short id
// is, so a user can't mistake it for garbage in the UI either.
func TestImagesGetShowsDigestRefInsteadOfNoneWhenTagsEmptyButDigestsArent(t *testing.T) {
	full := "sha256:" + fmt.Sprintf("%064x", 1)
	digest := "redis@sha256:" + fmt.Sprintf("%064x", 2)
	s := New(Options{Version: "test-1", Started: time.Now(), Images: func(context.Context) (ImagesDTO, error) {
		return ImagesDTO{Images: []ImageInfo{{ID: full, RepoDigests: []string{digest}, SizeBytes: 100, State: "unused"}}}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/images")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var dto ImagesDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Len(t, dto.Images, 1)
	require.Equal(t, []string{"redis@sha256:" + fmt.Sprintf("%064x", 2)[:12]}, dto.Images[0].RepoTags,
		"a digest-pinned image must show its (truncated) digest ref, not the untagged sentinel")
}

func TestImagesGetCarriesFullIDAlongsideShortID(t *testing.T) {
	full := "sha256:" + fmt.Sprintf("%064x", 1)
	s := New(Options{Version: "test-1", Started: time.Now(), Images: func(context.Context) (ImagesDTO, error) {
		return ImagesDTO{Images: []ImageInfo{{ID: full, SizeBytes: 100, State: "dangling"}}}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/images")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var dto ImagesDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Len(t, dto.Images, 1)
	require.Equal(t, full, dto.Images[0].FullID, "full_id must carry the untruncated id for mutating calls to use")
	require.Len(t, dto.Images[0].ID, 12, "id stays docker's own 12-char short form for display")
}

func TestImagesGetNeverBlockedByReadOnlyOrMissingConfirmHeader(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ReadOnly: true, Images: func(context.Context) (ImagesDTO, error) {
		return ImagesDTO{}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/images")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestImagesRemoveRequiresConfirmHeader(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["sha256:`+fmt.Sprintf("%064x", 1)+`"]}`, "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
}

// TestImagesConfirmHeaderValueIsCaseSensitive pins F7: the guardrail
// compares the header value with a plain Go string ==, not
// strings.EqualFold -- "IMAGES" must be rejected exactly like an empty
// or missing value, guarding against a future refactor loosening this
// without realizing it's a guardrail, not a UX nicety.
func TestImagesConfirmHeaderValueIsCaseSensitive(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["`+fmt.Sprintf("%064x", 1)+`"]}`, "IMAGES")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
}

// TestImagesOptionsMethodNeverReachesHandler pins F7: net/http's own
// ServeMux (Go 1.22+ method-specific patterns) 405s any method the
// route wasn't registered for, including OPTIONS -- confirmed here
// rather than just assumed, since a browser CORS preflight would send
// exactly this, and it must never run RemoveImages/PruneImages.
func TestImagesOptionsMethodNeverReachesHandler(t *testing.T) {
	var removeCalled, pruneCalled bool
	s := New(Options{
		Version: "test-1", Started: time.Now(),
		RemoveImages: func(context.Context, []string) ([]ImageRemoveResult, error) {
			removeCalled = true
			return nil, nil
		},
		PruneImages: func(context.Context, string) (ImagePruneResult, error) {
			pruneCalled = true
			return ImagePruneResult{}, nil
		},
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/images/remove", "/api/images/prune"} {
		req, err := http.NewRequest(http.MethodOptions, ts.URL+path, bytes.NewBufferString(`{"ids":["`+fmt.Sprintf("%064x", 1)+`"],"mode":"dangling"}`))
		require.NoError(t, err)
		req.Header.Set("X-Gantry-Confirm", "images")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "OPTIONS on %s", path)
	}
	require.False(t, removeCalled, "OPTIONS must never reach RemoveImages")
	require.False(t, pruneCalled, "OPTIONS must never reach PruneImages")
}

func TestImagesRemoveRefusesWhenReadOnly(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["sha256:`+fmt.Sprintf("%064x", 1)+`"]}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestImagesRemoveRejectsNonIDStrings(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["nginx:latest"]}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestImagesRemoveAcceptedIDForms pins F1: only a full 64-hex digest (bare
// or "sha256:"-prefixed) may reach the backend. Anything shorter -- even a
// syntactically plausible short id -- must 400 before ever calling
// RemoveImages: moby's own reference parser only recognizes a BARE
// identifier as a digest when it's exactly 64 hex chars (anchoredIdentifierRegexp
// in distribution/reference), so anything shorter falls through to name
// resolution instead of id-prefix resolution -- see imageIDPattern's own doc.
func TestImagesRemoveAcceptedIDForms(t *testing.T) {
	full := fmt.Sprintf("%064x", 1)
	cases := []struct {
		name   string
		id     string
		wantOK bool
	}{
		{"bare 64 hex", full, true},
		{"sha256-prefixed 64 hex", "sha256:" + full, true},
		{"12-char short id", full[:12], false},
		{"63-char hex, one short of full", full[:63], false},
		{"repo:tag name", "nginx:latest", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calledWith []string
			s := New(Options{
				Version: "test-1", Started: time.Now(),
				RemoveImages: func(_ context.Context, ids []string) ([]ImageRemoveResult, error) {
					calledWith = ids
					out := make([]ImageRemoveResult, len(ids))
					for i, id := range ids {
						out[i] = ImageRemoveResult{ID: id, OK: true}
					}
					return out, nil
				},
			})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["`+tc.id+`"]}`, "images")
			defer func() { _ = resp.Body.Close() }()

			if tc.wantOK {
				require.Equal(t, http.StatusOK, resp.StatusCode, "id form %q must be accepted", tc.id)
				require.Equal(t, []string{tc.id}, calledWith, "the backend must see exactly the id the caller sent")
			} else {
				require.Equal(t, http.StatusBadRequest, resp.StatusCode, "id form %q must be rejected before ever reaching the backend", tc.id)
				require.Nil(t, calledWith, "a rejected id must never reach RemoveImages")
			}
		})
	}
}

func TestImagesRemoveRejectsEmptyIDs(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":[]}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestImagesRemoveRejectsMoreThanMaxIDs pins F4: an unbounded ids array
// is a caller mistake (or something worse) either way -- reject it
// outright rather than passing 101+ ids through to the backend.
func TestImagesRemoveRejectsMoreThanMaxIDs(t *testing.T) {
	var calledWith []string
	s := New(Options{
		Version: "test-1", Started: time.Now(),
		RemoveImages: func(_ context.Context, ids []string) ([]ImageRemoveResult, error) {
			calledWith = ids
			return nil, nil
		},
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf(`"%064x"`, i)
	}
	body := `{"ids":[` + strings.Join(ids, ",") + `]}`

	resp := postImages(t, ts.URL+"/api/images/remove", body, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Nil(t, calledWith, "over the cap must never reach the backend")
}

// TestImagesRemoveRejectsOversizedBody and
// TestImagesPruneRejectsOversizedBody pin F4's other half: both mutating
// routes cap the request body itself (http.MaxBytesReader), not just
// the decoded ids count -- a caller can't force an unbounded read into
// memory before validation even gets a chance to run. The oversized
// part is leading JSON whitespace (insignificant, and skipped by the
// decoder) padded well past the 1MB cap around an otherwise entirely
// valid body -- so this fails ONLY if the byte-size cap itself is
// enforced, not merely because the payload was nonsense content. This
// is specifically a MaxBytesReader overflow (a *http.MaxBytesError),
// not a malformed body, so it must 413, not 400 -- see
// writeDecodeError's own doc.
func TestImagesRemoveRejectsOversizedBody(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	oversized := strings.Repeat(" ", 2<<20) + `{"ids":["` + fmt.Sprintf("%064x", 1) + `"]}`
	resp := postImages(t, ts.URL+"/api/images/remove", oversized, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body["error"], "too large")
}

func TestImagesPruneRejectsOversizedBody(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	oversized := strings.Repeat(" ", 2<<20) + `{"mode":"dangling"}`
	resp := postImages(t, ts.URL+"/api/images/prune", oversized, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body["error"], "too large")
}

// TestImagesRemoveNonOversizedBodyErrorStays400 guards the split above
// against over-firing: a malformed-but-not-oversized body (invalid
// JSON, well under imagesMaxRequestBytes) is a genuinely different
// problem than a MaxBytesReader overflow and must keep 400, not 413.
func TestImagesRemoveNonOversizedBodyErrorStays400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{not valid json`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestImagesRemove404WhenBackendNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["sha256:`+fmt.Sprintf("%064x", 1)+`"]}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestImagesRemoveMultiStatusPartialFailureAndLogsEventsOnlyForSuccess(t *testing.T) {
	goodID := "sha256:" + fmt.Sprintf("%064x", 1)
	badID := "sha256:" + fmt.Sprintf("%064x", 2)
	var appendCalls []eventCall

	s := New(Options{
		Version: "test-1", Started: time.Now(),
		RemoveImages: func(_ context.Context, ids []string) ([]ImageRemoveResult, error) {
			require.Equal(t, []string{goodID, badID}, ids)
			return []ImageRemoveResult{
				{ID: goodID, OK: true, RepoTags: []string{"app:latest"}, SizeBytes: 42},
				{ID: badID, OK: false, Error: "conflict: image is being used by a container"},
			}, nil
		},
		AppendEvent: capturingAppendEvent(&appendCalls),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":["`+goodID+`","`+badID+`"]}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "a per-item failure must not fail the whole request")

	var results []ImageRemoveResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&results))
	require.Equal(t, []ImageRemoveResult{
		{ID: goodID, OK: true},
		{ID: badID, OK: false, Error: "conflict: image is being used by a container"},
	}, results, "the wire response must carry exactly {id,ok,error} -- no repo_tags/size_bytes leak")

	require.Len(t, appendCalls, 1, "only the successful removal logs an event")
	require.Equal(t, "image.removed", appendCalls[0].Kind)
	require.Equal(t, "info", appendCalls[0].Severity, "explicit like every other lifecycle event (container.start, parity.start), not left at AppendEvent's own empty-string default")
	require.Contains(t, appendCalls[0].Detail, goodID, "the full id belongs in Detail for the audit trail -- Entity only ever carries the short form")
}

func TestImagesPruneRequiresConfirmHeader(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/prune", `{"mode":"dangling"}`, "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
}

func TestImagesPruneRefusesWhenReadOnly(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/prune", `{"mode":"dangling"}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestImagesPruneRejectsInvalidMode(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/prune", `{"mode":"all"}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestImagesPrune404WhenBackendNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/prune", `{"mode":"unused"}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestImagesPruneDeletesAndLogsEventPerDeletedImageForBothModes(t *testing.T) {
	for _, mode := range []string{"dangling", "unused"} {
		t.Run(mode, func(t *testing.T) {
			var gotMode string
			var appendCalls []eventCall
			s := New(Options{
				Version: "test-1", Started: time.Now(),
				PruneImages: func(_ context.Context, m string) (ImagePruneResult, error) {
					gotMode = m
					return ImagePruneResult{
						Deleted:        []DeletedImage{{ID: "sha256:abc", RepoTags: []string{"old:1"}, SizeBytes: 100}},
						ReclaimedBytes: 100,
						Errors:         []string{},
					}, nil
				},
				AppendEvent: capturingAppendEvent(&appendCalls),
			})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := postImages(t, ts.URL+"/api/images/prune", `{"mode":"`+mode+`"}`, "images")
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, mode, gotMode)

			var result ImagePruneResult
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
			require.Equal(t, int64(100), result.ReclaimedBytes)
			require.Len(t, result.Deleted, 1)
			require.Empty(t, result.Errors)

			require.Len(t, appendCalls, 1)
			require.Equal(t, "image.removed", appendCalls[0].Kind)
		})
	}
}
