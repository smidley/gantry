package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	Kind, Entity, Detail string
}

func capturingAppendEvent(calls *[]eventCall) func(store.Event) (int64, error) {
	return func(e store.Event) (int64, error) {
		*calls = append(*calls, eventCall{e.Kind, e.Entity, e.Detail})
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

func TestImagesRemoveRejectsEmptyIDs(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postImages(t, ts.URL+"/api/images/remove", `{"ids":[]}`, "images")
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
