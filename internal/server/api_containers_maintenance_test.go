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

	"github.com/stretchr/testify/require"
)

// postContainers issues a POST to path (either
// "/api/containers/maintenance/remove" or
// "/api/containers/maintenance/prune") with body as the raw JSON string
// -- mirrors postImages exactly, for the containers confirm value
// instead of images'.
func postContainers(t *testing.T, url, body, confirm string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue) // gate.go's cross-site check -- so confirm="" still reaches the route's own 428
	if confirm != "" {
		req.Header.Set("X-Gantry-Confirm", confirm)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestContainersMaintenanceGetDefaultsToEmptyWhenOptionsNil(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/maintenance")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var dto ContainerMaintenanceDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Empty(t, dto.Containers)
}

func TestContainersMaintenanceGetShortensIDAndCarriesFullID(t *testing.T) {
	full := fmt.Sprintf("%064x", 1)
	s := New(Options{Version: "test-1", Started: time.Now(), ContainersMaintenance: func(context.Context) (ContainerMaintenanceDTO, error) {
		return ContainerMaintenanceDTO{Containers: []ContainerMaintenanceInfo{{ID: full, Name: "web", State: "exited"}}}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/maintenance")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var dto ContainerMaintenanceDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Len(t, dto.Containers, 1)
	require.Len(t, dto.Containers[0].ID, 12, "GET must show docker's own 12-char short id, not the full id")
	require.Equal(t, full[:12], dto.Containers[0].ID)
	require.Equal(t, full, dto.Containers[0].FullID, "full_id must carry the untruncated id for mutating calls to use")
}

// TestContainersMaintenanceGetOmitsExitCodeAndFinishedAtWhenNil pins the
// *int/*int64 wire design: a created container (never inspected, see
// docker.ContainerMaintenanceInfo's own doc) must not show
// exit_code/finished_at keys at all, not zero values -- 0 is a real,
// meaningful exit code, so the two must be distinguishable on the wire.
func TestContainersMaintenanceGetOmitsExitCodeAndFinishedAtWhenNil(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ContainersMaintenance: func(context.Context) (ContainerMaintenanceDTO, error) {
		return ContainerMaintenanceDTO{Containers: []ContainerMaintenanceInfo{{ID: fmt.Sprintf("%064x", 1), Name: "runner", State: "created"}}}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/maintenance")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var raw []map[string]any
	body := struct {
		Containers []map[string]any `json:"containers"`
	}{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	raw = body.Containers
	require.Len(t, raw, 1)
	require.NotContains(t, raw[0], "exit_code")
	require.NotContains(t, raw[0], "finished_at")
}

func TestContainersMaintenanceGetIncludesExitCodeZeroExplicitly(t *testing.T) {
	zero := 0
	s := New(Options{Version: "test-1", Started: time.Now(), ContainersMaintenance: func(context.Context) (ContainerMaintenanceDTO, error) {
		return ContainerMaintenanceDTO{Containers: []ContainerMaintenanceInfo{{ID: fmt.Sprintf("%064x", 1), Name: "job", State: "exited", ExitCode: &zero}}}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/maintenance")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Containers []map[string]any `json:"containers"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body.Containers[0], "exit_code", "a clean exit (code 0) must still be present on the wire, not treated as absent")
	require.InDelta(t, 0, body.Containers[0]["exit_code"], 0)
}

func TestContainersMaintenanceGetNeverBlockedByReadOnlyOrMissingConfirmHeader(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ReadOnly: true, ContainersMaintenance: func(context.Context) (ContainerMaintenanceDTO, error) {
		return ContainerMaintenanceDTO{}, nil
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/maintenance")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestContainersMaintenanceOptionsMethodNeverReachesHandler(t *testing.T) {
	var removeCalled, pruneCalled bool
	s := New(Options{
		Version: "test-1", Started: time.Now(),
		RemoveContainers: func(context.Context, []string) ([]ContainerRemoveResult, error) {
			removeCalled = true
			return nil, nil
		},
		PruneContainers: func(context.Context, string, int) (ContainerPruneResult, error) {
			pruneCalled = true
			return ContainerPruneResult{}, nil
		},
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	full := fmt.Sprintf("%064x", 1)
	for _, path := range []string{"/api/containers/maintenance/remove", "/api/containers/maintenance/prune"} {
		req, err := http.NewRequest(http.MethodOptions, ts.URL+path, bytes.NewBufferString(`{"ids":["`+full+`"],"mode":"exited"}`))
		require.NoError(t, err)
		req.Header.Set("X-Gantry-Confirm", "containers")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "OPTIONS on %s", path)
	}
	require.False(t, removeCalled)
	require.False(t, pruneCalled)
}

func TestContainersRemoveRequiresConfirmHeader(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	full := fmt.Sprintf("%064x", 1)
	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":["`+full+`"]}`, "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
}

// TestContainersRemoveWrongConfirmValueIsRejected pins that the
// containers routes check the CONTAINERS-scoped value, not images' --
// see containersConfirmValue's own doc for why the value is
// resource-scoped in the first place.
func TestContainersRemoveWrongConfirmValueIsRejected(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	full := fmt.Sprintf("%064x", 1)
	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":["`+full+`"]}`, "images")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusPreconditionRequired, resp.StatusCode, "the images confirm value must not also satisfy the containers route")
}

func TestContainersRemoveRefusesWhenReadOnly(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	full := fmt.Sprintf("%064x", 1)
	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":["`+full+`"]}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestContainersPruneRequiresConfirmHeader(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"exited"}`, "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusPreconditionRequired, resp.StatusCode)
}

func TestContainersPruneRefusesWhenReadOnly(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"exited"}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestContainersRemoveIDPatternRejectionTable pins F1's container
// analogue: only a bare, EXACTLY 64-character lowercase-hex container id
// may reach the backend -- unlike images, there is no "sha256:"-prefixed
// alternate form at all (see containerIDPattern's own doc), so that form
// must be rejected here, not accepted.
func TestContainersRemoveIDPatternRejectionTable(t *testing.T) {
	full := fmt.Sprintf("%064x", 1)
	cases := []struct {
		name   string
		id     string
		wantOK bool
	}{
		{"bare 64 hex", full, true},
		{"sha256-prefixed 64 hex (not a valid container id form)", "sha256:" + full, false},
		{"12-char short id", full[:12], false},
		{"63-char hex, one short of full", full[:63], false},
		{"uppercase hex", strings.ToUpper(fmt.Sprintf("%064x", 0xdeadbeef)), false},
		{"container name", "my-web-container", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calledWith []string
			s := New(Options{
				Version: "test-1", Started: time.Now(),
				RemoveContainers: func(_ context.Context, ids []string) ([]ContainerRemoveResult, error) {
					calledWith = ids
					out := make([]ContainerRemoveResult, len(ids))
					for i, id := range ids {
						out[i] = ContainerRemoveResult{ID: id, OK: true}
					}
					return out, nil
				},
			})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":["`+tc.id+`"]}`, "containers")
			defer func() { _ = resp.Body.Close() }()

			if tc.wantOK {
				require.Equal(t, http.StatusOK, resp.StatusCode, "id form %q must be accepted", tc.id)
				require.Equal(t, []string{tc.id}, calledWith)
			} else {
				require.Equal(t, http.StatusBadRequest, resp.StatusCode, "id form %q must be rejected before ever reaching the backend", tc.id)
				require.Nil(t, calledWith, "a rejected id must never reach RemoveContainers")
			}
		})
	}
}

func TestContainersRemoveRejectsEmptyIDs(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":[]}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestContainersRemoveRejectsMoreThanMaxIDs(t *testing.T) {
	var calledWith []string
	s := New(Options{
		Version: "test-1", Started: time.Now(),
		RemoveContainers: func(_ context.Context, ids []string) ([]ContainerRemoveResult, error) {
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

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", body, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Nil(t, calledWith)
}

func TestContainersRemoveRejectsOversizedBody(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	oversized := strings.Repeat(" ", 2<<20) + `{"ids":["` + fmt.Sprintf("%064x", 1) + `"]}`
	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", oversized, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestContainersPruneRejectsOversizedBody(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	oversized := strings.Repeat(" ", 2<<20) + `{"mode":"exited"}`
	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", oversized, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestContainersRemove404WhenBackendNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	full := fmt.Sprintf("%064x", 1)
	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":["`+full+`"]}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestContainersPrune404WhenBackendNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"exited"}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestContainersRemoveMultiStatusPartialFailureAndLogsEventsOnlyForSuccess
// pins the multi-status envelope plus the running-id conflict
// passthrough (bullet 2): a running container's own docker conflict
// error must reach the caller completely unmodified, in the same
// response as a successful removal, without failing the whole request.
func TestContainersRemoveMultiStatusPartialFailureAndLogsEventsOnlyForSuccess(t *testing.T) {
	goodID := fmt.Sprintf("%064x", 1)
	runningID := fmt.Sprintf("%064x", 2)
	runningConflict := `conflict: cannot remove container "web": container is running`
	var appendCalls []eventCall

	s := New(Options{
		Version: "test-1", Started: time.Now(),
		RemoveContainers: func(_ context.Context, ids []string) ([]ContainerRemoveResult, error) {
			require.Equal(t, []string{goodID, runningID}, ids)
			return []ContainerRemoveResult{
				{ID: goodID, OK: true, Name: "duplicati", Image: "lscr.io/linuxserver/duplicati:latest"},
				{ID: runningID, OK: false, Error: runningConflict},
			}, nil
		},
		AppendEvent: capturingAppendEvent(&appendCalls),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/remove", `{"ids":["`+goodID+`","`+runningID+`"]}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "a per-item failure must not fail the whole request")

	var results []ContainerRemoveResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&results))
	require.Equal(t, []ContainerRemoveResult{
		{ID: goodID, OK: true},
		{ID: runningID, OK: false, Error: runningConflict},
	}, results, "the wire response must carry exactly {id,ok,error} -- no name/image leak, and the conflict message must be verbatim")

	require.Len(t, appendCalls, 1, "only the successful removal logs an event")
	require.Equal(t, "container.removed", appendCalls[0].Kind)
	require.Equal(t, "info", appendCalls[0].Severity)
	require.Contains(t, appendCalls[0].Detail, "duplicati")
	require.Contains(t, appendCalls[0].Detail, "lscr.io/linuxserver/duplicati:latest")
	require.Contains(t, appendCalls[0].Detail, goodID, "the full id belongs in Detail for the audit trail")
}

func TestContainersPruneRejectsInvalidMode(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"all"}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestContainersPruneRejectsNegativeOlderThanHours(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"exited","older_than_hours":-1}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestContainersPruneDeletesAndLogsEventPerDeletedContainerForEveryMode
// pins all three prune modes plus older_than_hours' pass-through to the
// backend, which is where selectPruneTargets' own filtering actually
// happens (docker package) -- the server layer's job is just to
// validate and forward.
func TestContainersPruneDeletesAndLogsEventPerDeletedContainerForEveryMode(t *testing.T) {
	for _, mode := range []string{"exited", "created", "all-stopped"} {
		t.Run(mode, func(t *testing.T) {
			var gotMode string
			var gotOlderThan int
			var appendCalls []eventCall
			s := New(Options{
				Version: "test-1", Started: time.Now(),
				PruneContainers: func(_ context.Context, m string, olderThanHours int) (ContainerPruneResult, error) {
					gotMode = m
					gotOlderThan = olderThanHours
					return ContainerPruneResult{
						Deleted: []DeletedContainer{{ID: fmt.Sprintf("%064x", 1), Name: "old-runner", Image: "ghcr.io/actions/actions-runner:latest"}},
						Errors:  []string{},
					}, nil
				},
				AppendEvent: capturingAppendEvent(&appendCalls),
			})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"`+mode+`","older_than_hours":24}`, "containers")
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, mode, gotMode)
			require.Equal(t, 24, gotOlderThan)

			var result ContainerPruneResult
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
			require.Len(t, result.Deleted, 1)
			require.Empty(t, result.Errors)

			require.Len(t, appendCalls, 1)
			require.Equal(t, "container.removed", appendCalls[0].Kind)
		})
	}
}

func TestContainersPruneDefaultsOlderThanHoursToZeroWhenOmitted(t *testing.T) {
	var gotOlderThan = -1
	s := New(Options{
		Version: "test-1", Started: time.Now(),
		PruneContainers: func(_ context.Context, _ string, olderThanHours int) (ContainerPruneResult, error) {
			gotOlderThan = olderThanHours
			return ContainerPruneResult{}, nil
		},
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postContainers(t, ts.URL+"/api/containers/maintenance/prune", `{"mode":"exited"}`, "containers")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 0, gotOlderThan)
}
