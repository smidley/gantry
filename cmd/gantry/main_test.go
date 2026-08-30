package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/collect/gpu"
	"github.com/smidley/gantry/internal/collect/host"
	"github.com/smidley/gantry/internal/collect/unraid"
	"github.com/smidley/gantry/internal/config"
	"github.com/smidley/gantry/internal/fake"
	"github.com/smidley/gantry/internal/server"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRunServesHealthzAndShutsDown(t *testing.T) {
	port := freePort(t)
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   filepath.Join(t.TempDir(), "g.db"),
		"GANTRY_FAKE_DATA": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	// Sources must reflect the real collector registry now (Task 15), not
	// the Phase 1 empty placeholder — every registered collector's name is
	// a key regardless of its Probe outcome. Most real data sources are
	// expected to be unavailable on this test box (no /proc, no
	// /host/sys, no nvidia-smi) — that unavailability, with its hint
	// Detail, is the degradation model working as designed — so only key
	// presence is asserted, not any particular value.
	healthzResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
	require.NoError(t, err)
	var healthzBody struct {
		Sources map[string]string `json:"sources"`
	}
	require.NoError(t, json.NewDecoder(healthzResp.Body).Decode(&healthzBody))
	drainAndClose(healthzResp)
	require.NotEmpty(t, healthzBody.Sources, "healthz sources must not be the empty placeholder once collectors are wired")
	for _, name := range []string{"host", "docker", "unraid"} {
		_, ok := healthzBody.Sources[name]
		require.True(t, ok, "sources must include %q", name)
	}

	// Snapshot smoke check: with fake data on, the fake generator writes
	// host/container series through the same store the real collectors
	// use, so any particular METRIC value's presence is still a timing
	// accident (has a tick landed yet?) — but the fake fleet's inventory
	// itself (Task 11's Metas()/fakeMetas wiring) is NOT: it's seeded
	// into every frame the same unconditional way dc.Running() seeds a
	// real fleet, independent of whether any sample has been recorded
	// yet. So container PRESENCE and state are asserted deterministically
	// here; only response shape is asserted for the rest.
	snapResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/live/snapshot", port))
	require.NoError(t, err)
	var snapBody struct {
		TS         int64 `json:"ts"`
		Containers map[string]struct {
			State string `json:"state"`
		} `json:"containers"`
	}
	require.NoError(t, json.NewDecoder(snapResp.Body).Decode(&snapBody))
	drainAndClose(snapResp)
	require.Equal(t, http.StatusOK, snapResp.StatusCode)
	require.Greater(t, snapBody.TS, int64(0))
	require.NotEmpty(t, snapBody.Containers, "fake mode's Metas() must survive the DTO-v2 container filter unconditionally")
	jf, ok := snapBody.Containers["jellyfin"]
	require.True(t, ok, "fake fleet member must appear in the frame")
	require.Equal(t, "running", jf.State)

	// /api/series smoke check: Query is now wired straight to
	// st.QuerySeries (Task 7) -- only the response shape (200, JSON array,
	// one entry per requested metric) is asserted, not its data, since
	// whether anything has flushed to samples_1m yet is a timing accident.
	seriesResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/series?kind=host&metrics=cpu.total", port))
	require.NoError(t, err)
	var seriesBody []struct {
		Metric string `json:"metric"`
	}
	require.NoError(t, json.NewDecoder(seriesResp.Body).Decode(&seriesBody))
	drainAndClose(seriesResp)
	require.Equal(t, http.StatusOK, seriesResp.StatusCode)
	require.Len(t, seriesBody, 1)
	require.Equal(t, "cpu.total", seriesBody[0].Metric)

	// /api/live smoke check: Live/Current are now wired (Task 8) --
	// read just the connect frame's SSE event (up to the blank line that
	// ends it) then close, rather than waiting on the 2s publish loop. A
	// dedicated, timeout-bound client keeps a handler bug (e.g. never
	// writing the connect frame) a prompt test failure, not a hang.
	sseClient := &http.Client{Timeout: 5 * time.Second}
	liveResp, err := sseClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/live", port))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, liveResp.StatusCode)
	require.Equal(t, "text/event-stream", liveResp.Header.Get("Content-Type"))
	reader := bufio.NewReader(liveResp.Body)
	sawData := false
	for {
		line, rerr := reader.ReadString('\n')
		require.NoError(t, rerr)
		if strings.HasPrefix(line, "data: ") {
			sawData = true
		}
		if strings.TrimRight(line, "\n") == "" {
			break // blank line: end of the connect frame's SSE event
		}
	}
	require.True(t, sawData, "connect frame must carry a data: line")
	_ = liveResp.Body.Close() // unread rest of body: transport closes the connection, freeing the Broadcaster slot

	// /api/containers/{name}/logs smoke check (Task 9): Logs is now wired
	// to the real dc.StreamLogs, but fake-data mode's synthetic
	// containers never touch dc's registry (the fake generator writes
	// straight to the store, bypassing the docker collector entirely) --
	// so a fake container name must 404 gracefully, exactly the contract
	// the fake-mode log viewer relies on, rather than erroring some other
	// way once a real Logs closure is wired.
	logsResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/containers/jellyfin/logs", port))
	require.NoError(t, err)
	drainAndClose(logsResp)
	require.Equal(t, http.StatusNotFound, logsResp.StatusCode, "a fake-mode container name must 404, not error, against the real Logs closure")

	// /api/settings smoke check (Task 10): exercises the real
	// settingsAdapter end to end (config resolution + SettingSet), not
	// just a fake. GET first reflects the compiled defaults with
	// nothing overridden (none of the four retention env vars are set
	// for this run), then a PUT's new value is visible on the very next
	// GET -- the read path doesn't wait on the maintenance loop's own
	// 10-minute tick, only Maintain's actual retention-pruning behavior
	// does (see the per-tick RetentionFromConfig comment above).
	settingsResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port))
	require.NoError(t, err)
	var settingsBody struct {
		Retention struct {
			R1Hours int `json:"r1_hours"`
		} `json:"retention"`
		EnvOverridden []string `json:"env_overridden"`
	}
	require.NoError(t, json.NewDecoder(settingsResp.Body).Decode(&settingsBody))
	drainAndClose(settingsResp)
	require.Equal(t, http.StatusOK, settingsResp.StatusCode)
	require.Equal(t, 48, settingsBody.Retention.R1Hours, "default r1_hours")
	require.Empty(t, settingsBody.EnvOverridden)

	putReq, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/api/settings", port),
		strings.NewReader(`{"retention":{"r1_hours":72,"r2_days":30,"r3_days":390,"size_cap_mb":512}}`))
	require.NoError(t, err)
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	drainAndClose(putResp)
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	settingsResp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port))
	require.NoError(t, err)
	var settingsBody2 struct {
		Retention struct {
			R1Hours int `json:"r1_hours"`
		} `json:"retention"`
	}
	require.NoError(t, json.NewDecoder(settingsResp2.Body).Decode(&settingsBody2))
	drainAndClose(settingsResp2)
	require.Equal(t, 72, settingsBody2.Retention.R1Hours, "PUT must persist through the real store, visible on the very next GET")

	// /api/groups smoke check: exercises the real groupsAdapter end to
	// end, same shape as the settings check just above -- groupsAdapter
	// talks straight to the same *store.Store fake mode already uses for
	// everything else (there's no separate fake-mode groups store to
	// diverge from), so this also proves groups persistence works
	// unconditionally in fake mode, not just for real installs. GET
	// starts empty (nothing saved yet), and a PUT's new value is visible
	// on the very next GET.
	type groupsWire struct {
		Groups []server.Group `json:"groups"`
	}
	groupsResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/groups", port))
	require.NoError(t, err)
	var groupsBody groupsWire
	require.NoError(t, json.NewDecoder(groupsResp.Body).Decode(&groupsBody))
	drainAndClose(groupsResp)
	require.Equal(t, http.StatusOK, groupsResp.StatusCode)
	require.Empty(t, groupsBody.Groups, "nothing saved yet")

	groupsPutReq, err := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/api/groups", port),
		strings.NewReader(`{"groups":[{"name":"media","members":["jellyfin","sonarr"]}]}`))
	require.NoError(t, err)
	groupsPutResp, err := http.DefaultClient.Do(groupsPutReq)
	require.NoError(t, err)
	drainAndClose(groupsPutResp)
	require.Equal(t, http.StatusOK, groupsPutResp.StatusCode)

	groupsResp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/groups", port))
	require.NoError(t, err)
	var groupsBody2 groupsWire
	require.NoError(t, json.NewDecoder(groupsResp2.Body).Decode(&groupsBody2))
	drainAndClose(groupsResp2)
	require.Equal(t, []server.Group{{Name: "media", Members: []string{"jellyfin", "sonarr"}}}, groupsBody2.Groups,
		"PUT must persist through the real store, visible on the very next GET")

	// /api/images smoke check: exercises the real fake.Generator-backed
	// Images/RemoveImages/PruneImages wiring end to end (fake mode has no
	// real docker daemon, so this is the ONLY way that selection is ever
	// exercised against a live server). GET first sees the full fake
	// seed, a prune("unused") removes some of it, and a targeted
	// remove of one still-dangling id removes exactly that one --
	// together proving both mutating routes actually reach the fake
	// inventory, not just validate and stop.
	imagesResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/images", port))
	require.NoError(t, err)
	var imagesBody struct {
		Images []struct {
			ID     string `json:"id"`
			FullID string `json:"full_id"`
			State  string `json:"state"`
		} `json:"images"`
		Summary struct {
			Unused int    `json:"unused"`
			Note   string `json:"note"`
		} `json:"summary"`
	}
	require.NoError(t, json.NewDecoder(imagesResp.Body).Decode(&imagesBody))
	drainAndClose(imagesResp)
	require.Equal(t, http.StatusOK, imagesResp.StatusCode)
	require.Len(t, imagesBody.Images, 13, "fake mode's own image seed")
	require.NotEmpty(t, imagesBody.Summary.Note)
	var danglingFullID string
	for _, im := range imagesBody.Images {
		if im.State == "dangling" {
			danglingFullID = im.FullID
			break
		}
	}
	require.NotEmpty(t, danglingFullID, "mutating calls use full_id, not GET's own display-only short id")

	pruneResp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/images/prune", port), "application/json", strings.NewReader(`{"mode":"unused"}`))
	require.NoError(t, err)
	drainAndClose(pruneResp)
	// No X-Gantry-Confirm header on this request: must 428, proving the
	// guardrail is really wired into the live route, not bypassed.
	require.Equal(t, http.StatusPreconditionRequired, pruneResp.StatusCode)

	pruneReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/images/prune", port), strings.NewReader(`{"mode":"unused"}`))
	require.NoError(t, err)
	pruneReq.Header.Set("X-Gantry-Confirm", "images")
	pruneResp2, err := http.DefaultClient.Do(pruneReq)
	require.NoError(t, err)
	var pruneBody struct {
		Deleted []struct {
			ID string `json:"id"`
		} `json:"deleted"`
	}
	require.NoError(t, json.NewDecoder(pruneResp2.Body).Decode(&pruneBody))
	drainAndClose(pruneResp2)
	require.Equal(t, http.StatusOK, pruneResp2.StatusCode)
	require.Len(t, pruneBody.Deleted, imagesBody.Summary.Unused, "must delete every currently-unused fake image")

	removeReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/images/remove", port), strings.NewReader(`{"ids":["`+danglingFullID+`"]}`))
	require.NoError(t, err)
	removeReq.Header.Set("X-Gantry-Confirm", "images")
	removeResp, err := http.DefaultClient.Do(removeReq)
	require.NoError(t, err)
	var removeBody []struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
	}
	require.NoError(t, json.NewDecoder(removeResp.Body).Decode(&removeBody))
	drainAndClose(removeResp)
	require.Equal(t, http.StatusOK, removeResp.StatusCode)
	require.Equal(t, []struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
	}{{ID: danglingFullID, OK: true}}, removeBody)

	imagesResp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/images", port))
	require.NoError(t, err)
	var imagesBody2 struct {
		Images []struct{} `json:"images"`
	}
	require.NoError(t, json.NewDecoder(imagesResp2.Body).Decode(&imagesBody2))
	drainAndClose(imagesResp2)
	require.Len(t, imagesBody2.Images, 13-len(pruneBody.Deleted)-1, "both the prune and the remove must have actually mutated the fake inventory")

	// Every request above went through http.DefaultTransport, which keeps
	// the underlying connection open (keep-alive) for reuse even after its
	// response body is drained and closed. An idle-but-open connection
	// held by this test's own client races against the server's graceful
	// Shutdown (server.go's ListenAndServe, 5s budget) as it waits for
	// connections to go idle — closing them from the client side first
	// removes that race rather than papering over it with a longer budget.
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("run did not shut down\n%s", buf[:n])
	}
}

// drainAndClose fully reads resp.Body before closing it so
// http.DefaultTransport can safely reuse (or promptly release) the
// underlying connection — an unread body left for Close alone can leave
// the connection's reuse state ambiguous to the transport.
func drainAndClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// TestRunReadOnlyModeBlocksImageMutationsButNotGet pins GANTRY_READ_ONLY's
// wiring through the real config resolver (cfg.Bool("read_only", ...),
// same envName mapping every other GANTRY_* setting uses) all the way to
// Options.ReadOnly: a mutating route must 403 even with a correct
// X-Gantry-Confirm header, while GET /api/images is unaffected.
func TestRunReadOnlyModeBlocksImageMutationsButNotGet(t *testing.T) {
	port := freePort(t)
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   filepath.Join(t.TempDir(), "g.db"),
		"GANTRY_FAKE_DATA": "1",
		"GANTRY_READ_ONLY": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	getResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/images", port))
	require.NoError(t, err)
	drainAndClose(getResp)
	require.Equal(t, http.StatusOK, getResp.StatusCode, "GET must stay available in read-only mode")

	pruneReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/images/prune", port), strings.NewReader(`{"mode":"unused"}`))
	require.NoError(t, err)
	pruneReq.Header.Set("X-Gantry-Confirm", "images")
	pruneResp, err := http.DefaultClient.Do(pruneReq)
	require.NoError(t, err)
	drainAndClose(pruneResp)
	require.Equal(t, http.StatusForbidden, pruneResp.StatusCode)

	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// TestRunWiresContainerMaintenanceThroughFakeDataEndToEnd pins main's own
// containersMaintenanceSrc/removeContainersSrc/pruneContainersSrc wiring
// (buildContainersMaintenance/buildRemoveContainers), not just the
// server package's own handler behavior (already covered without a real
// run() at all): GET must return the real fake seed's own
// "duplicati" entry, and removing it by the id GET actually handed back
// must succeed end to end -- a broken or missing adapter wiring would
// otherwise still return 200s (Options fields default to empty/404, not
// a compile error), so only exercising real data through the full stack
// catches it.
func TestRunWiresContainerMaintenanceThroughFakeDataEndToEnd(t *testing.T) {
	port := freePort(t)
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   filepath.Join(t.TempDir(), "g.db"),
		"GANTRY_FAKE_DATA": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	getResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/containers/maintenance", port))
	require.NoError(t, err)
	var dto server.ContainerMaintenanceDTO
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&dto))
	drainAndClose(getResp)
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var duplicatiID string
	for _, ct := range dto.Containers {
		if ct.Name == "duplicati" {
			duplicatiID = ct.FullID
		}
	}
	require.NotEmpty(t, duplicatiID, "fake mode's seed must include a duplicati entry")

	removeReq, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/containers/maintenance/remove", port),
		strings.NewReader(`{"ids":["`+duplicatiID+`"]}`))
	require.NoError(t, err)
	removeReq.Header.Set("X-Gantry-Confirm", "containers")
	removeResp, err := http.DefaultClient.Do(removeReq)
	require.NoError(t, err)
	var results []server.ContainerRemoveResult
	require.NoError(t, json.NewDecoder(removeResp.Body).Decode(&results))
	drainAndClose(removeResp)
	require.Equal(t, http.StatusOK, removeResp.StatusCode)
	require.Equal(t, []server.ContainerRemoveResult{{ID: duplicatiID, OK: true}}, results)

	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// TestRunReadOnlyModeBlocksContainerMaintenanceMutationsButNotGet mirrors
// TestRunReadOnlyModeBlocksImageMutationsButNotGet exactly, for the
// container-maintenance write path.
func TestRunReadOnlyModeBlocksContainerMaintenanceMutationsButNotGet(t *testing.T) {
	port := freePort(t)
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   filepath.Join(t.TempDir(), "g.db"),
		"GANTRY_FAKE_DATA": "1",
		"GANTRY_READ_ONLY": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	getResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/containers/maintenance", port))
	require.NoError(t, err)
	drainAndClose(getResp)
	require.Equal(t, http.StatusOK, getResp.StatusCode, "GET must stay available in read-only mode")

	pruneReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/containers/maintenance/prune", port), strings.NewReader(`{"mode":"exited"}`))
	require.NoError(t, err)
	pruneReq.Header.Set("X-Gantry-Confirm", "containers")
	pruneResp, err := http.DefaultClient.Do(pruneReq)
	require.NoError(t, err)
	drainAndClose(pruneResp)
	require.Equal(t, http.StatusForbidden, pruneResp.StatusCode)

	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// TestBuildSnapshotGroupsSamplesByKindAndSkipsLivePrefixed exercises the
// real buildSnapshot closure used in main wiring — offline, with no
// daemon or /proc required: dc.Running()/ur.Version() on freshly
// constructed (never-ticked) collectors deterministically return their
// zero state, so this only needs a real *store.Store fed by hand.
func TestBuildSnapshotGroupsSamplesByKindAndSkipsLivePrefixed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	st.Record(store.SeriesKey{Kind: "host", Metric: "cpu.total"}, 1000, 12.5)
	st.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "cpu.pct"}, 1000, 4.2)
	st.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sda.read_bps"}, 1000, 55555)
	st.Record(store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}, 1000, 31)
	st.Record(store.SeriesKey{Kind: "gpu", Entity: "gpu0", Metric: "engine.render.busy_pct"}, 1000, 5.5)
	st.Record(store.SeriesKey{Kind: "unraid", Entity: "docker", Metric: "docker.images_bytes"}, 1000, 999)
	st.Record(store.SeriesKey{Kind: "unraid", Entity: "array", Metric: "parity.progress_pct"}, 1000, 0)

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{"host": "ok"} }

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, nil, nil)() // nil fakeMetas/fakeDiskMeta: not exercising the fake-mode path here

	require.Equal(t, 12.5, snap.Host["cpu.total"])
	require.Equal(t, 31.0, snap.Disks["disk1"]["temp.c"])
	require.Equal(t, 5.5, snap.GPU["gpu0"]["engine.render.busy_pct"])
	require.Equal(t, "", snap.UnraidVersion, "unraid.Version() before any Tick is empty")
	require.Empty(t, dc.Running(), "docker.Running() before any Tick is empty")
	require.Equal(t, map[string]string{"host": "ok"}, snap.Sources, "v2: Sources rides in the frame")

	// v2: Unraid is entity-dimensioned -- docker.img usage and array/parity
	// data must land in separate buckets, not collide into one flat map.
	require.Equal(t, 999.0, snap.Unraid["docker"]["docker.images_bytes"])
	require.Equal(t, 0.0, snap.Unraid["array"]["parity.progress_pct"])
	require.Len(t, snap.Unraid, 2)

	// jellyfin is unknown to dc's registry (never ticked, and no fakeMetas
	// wired here) -- its ancient (year-1970) sample must not resurrect it
	// into the frame just because a sample happens to exist. See
	// TestBuildSnapshotIncludesStoppedContainerWithEmptyMetrics below for
	// the flip side: a container the registry DOES know about, but isn't
	// running, must still appear.
	_, stillPresent := snap.Containers["jellyfin"]
	require.False(t, stillPresent, "a container dc doesn't know about must not appear in the frame")

	for _, c := range snap.Containers {
		for metric := range c.Metrics {
			require.False(t, strings.HasPrefix(metric, "live:"), "live:-prefixed metrics must never reach the snapshot")
		}
	}
}

// TestBuildSnapshotIncludesFakeMetasWhenWired pins the ledger-carried
// Batch B fix (folded into Task 11): fake mode's synthetic containers
// never touch dc's registry at all -- the fake generator writes
// straight to the store, bypassing docker's registry entirely -- so
// without fakeMetas, Task 4's DTO-v2 filter (only dc.Running() OR a
// name with BOTH a fresh live sample AND a known Meta) would empty
// every fake-mode frame even though the store has live fake-container
// samples. fakeMetas must be treated exactly like dc.Running()'s real
// entries: unconditionally seeded, not merely consulted as a fallback
// lookup.
func TestBuildSnapshotIncludesFakeMetasWhenWired(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	st.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "cpu.pct"}, time.Now().Unix(), 4.2)

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "jellyfin", State: "running", Health: "healthy", Image: "demo/jellyfin:latest"}}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	require.Empty(t, dc.Running(), "dc's own registry never saw this container -- the fix must not depend on it")
	c, ok := snap.Containers["jellyfin"]
	require.True(t, ok, "a fake Meta must survive the DTO-v2 filter unconditionally, not as a fallback lookup")
	require.Equal(t, "running", c.State)
	require.Equal(t, "healthy", c.Health)
	require.Equal(t, "demo/jellyfin:latest", c.Image)
	require.Equal(t, 4.2, c.Metrics["cpu.pct"])
}

// TestBuildSnapshotMapsMetaBadgeAndNetworkFieldsIntoContainerDTO pins
// buildSnapshot's own field-by-field copy of Meta's badge/changelog/
// network/port fields into ContainerDTO -- the same copy State/Health/
// Image/Icon already get, just for this round's additions. Each of
// docker.Meta's own producer functions (metaFromInspect, extractNetworks/
// extractPorts, changelogAndProjectURLs, joinUpdateStatus) has its own
// tests pinning the values themselves; this pins that buildSnapshot
// actually wires them through to the DTO once Meta carries them.
func TestBuildSnapshotMapsMetaBadgeAndNetworkFieldsIntoContainerDTO(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	created := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{
			Name: "jellyfin", State: "running", Health: "healthy", Image: "jellyfin/jellyfin:latest",
			Created:      created,
			UpdateStatus: "available",
			ChangelogURL: "https://github.com/jellyfin/jellyfin-packaging/releases",
			ProjectURL:   "https://jellyfin.org",
			WebUIURL:     "http://[IP]:[PORT:8096]/",
			Networks:     []docker.NetworkInfo{{Name: "bridge", IP: "172.17.0.2"}},
			Ports:        []docker.PortInfo{{ContainerPort: 8096, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8096}},
		}}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	c, ok := snap.Containers["jellyfin"]
	require.True(t, ok)
	require.Equal(t, created.Unix(), c.Created)
	require.Equal(t, "available", c.UpdateStatus)
	require.Equal(t, "https://github.com/jellyfin/jellyfin-packaging/releases", c.ChangelogURL)
	require.Equal(t, "https://jellyfin.org", c.ProjectURL)
	require.Equal(t, "http://[IP]:[PORT:8096]/", c.WebUIURL)
	require.Equal(t, []server.NetworkInfoDTO{{Name: "bridge", IP: "172.17.0.2"}}, c.Networks)
	require.Equal(t, []server.PortInfoDTO{{ContainerPort: 8096, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8096}}, c.Ports)
}

// TestBuildSnapshotZeroCreatedOmittedNotEpochGarbage pins the zero-time
// guard: Go's zero time.Time.Unix() is a large negative number (year 1),
// not 0 -- a container Meta never populated Created (metaFromInspect
// couldn't parse it, or fake mode doesn't set it) must map to
// ContainerDTO.Created == 0, not that garbage value.
func TestBuildSnapshotZeroCreatedOmittedNotEpochGarbage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "jellyfin", State: "running"}} // Created left at its zero value
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	require.Equal(t, int64(0), snap.Containers["jellyfin"].Created)
}

// TestBuildSnapshotDropsStaleSampleFromRunningContainer pins the
// per-sample freshness gate a running container's metrics still need:
// buildSnapshot's own entity-membership seeding only decides whether the
// ENTITY belongs in the frame, so a still-running container's own
// individual samples were previously included unconditionally, no
// matter how old. That let
// a metric that stops being emitted (e.g. `docker update --memory 0`
// clearing mem.limit_bytes) serve its last recorded value as "current"
// forever. A sample this stale must be dropped even though its
// container is very much still in the frame; a fresh sibling metric on
// the same entity must be unaffected.
func TestBuildSnapshotDropsStaleSampleFromRunningContainer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().Unix()
	st.Record(store.SeriesKey{Kind: "container", Entity: "db", Metric: "mem.limit_bytes"}, now-90, 1e9) // stale: older than containerFrameMaxAge
	st.Record(store.SeriesKey{Kind: "container", Entity: "db", Metric: "mem.bytes"}, now-5, 5e8)        // fresh sibling on the same entity

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "db", State: "running"}} // running unconditionally, per buildSnapshot's own entity-level contract
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	c, ok := snap.Containers["db"]
	require.True(t, ok)
	_, hasStale := c.Metrics["mem.limit_bytes"]
	require.False(t, hasStale, "a >containerFrameMaxAge-old sample must not linger just because its container is still running")
	require.Equal(t, 5e8, c.Metrics["mem.bytes"], "a fresh sibling metric on the same entity must still come through")
}

// TestBuildSnapshotIncludesStoppedContainerWithEmptyMetrics pins the
// stopped-container fix: a container the registry knows about but that
// isn't running (fakeMetas stands in for dc.All() here, same convention
// as the fake-Metas test above) must still appear in the frame with its
// real state/identity, and with no metrics leaking in from before it
// stopped -- the store has no sample for it at all in this test, so its
// Metrics map coming back empty also proves buildSnapshot doesn't
// fabricate one.
func TestBuildSnapshotIncludesStoppedContainerWithEmptyMetrics(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "gitea", State: "exited", Health: "", Image: "demo/gitea:latest"}}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	c, ok := snap.Containers["gitea"]
	require.True(t, ok, "a stopped-but-known container must still appear in the frame")
	require.Equal(t, "exited", c.State)
	require.Equal(t, "demo/gitea:latest", c.Image)
	require.Empty(t, c.Metrics, "no live samples were recorded for it -- Metrics must not be fabricated")
}

// TestBuildSnapshotPassesThroughComposeProject pins the compare view's own
// Groups-chip data source: a Meta's ComposeProject (label passthrough,
// see registry_test.go's TestMetaFromInspectExtractsComposeProjectLabel)
// must survive into ContainerDTO unchanged, straight alongside Icon --
// same fakeMetas convention as TestBuildSnapshotIncludesFakeMetasWhenWired
// above -- and a Meta with no compose project must come through as "",
// not an absent/zero-value surprise.
func TestBuildSnapshotPassesThroughComposeProject(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{
			{Name: "gridmind-api", State: "running", Health: "healthy", Image: "demo/gridmind-api:latest", ComposeProject: "gridmind-cloud"},
			{Name: "jellyfin", State: "running", Health: "healthy", Image: "demo/jellyfin:latest"}, // no compose project
		}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	require.Equal(t, "gridmind-cloud", snap.Containers["gridmind-api"].ComposeProject)
	require.Equal(t, "", snap.Containers["jellyfin"].ComposeProject)
}

// TestBuildSnapshotPassesThroughCpusetAndExitCode pins Container Detail's
// two Go passthroughs (the anomaly banner's exit code, the Limits card's
// cpuset pin) exactly the same "straight through from Meta, no math"
// shape ComposeProject's own test above already pins -- an unpinned/
// still-running container must read back empty/zero, not some stale or
// invented value.
func TestBuildSnapshotPassesThroughCpusetAndExitCode(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{
			{Name: "minecraft", State: "running", Health: "healthy", Image: "demo/minecraft:latest", Cpuset: "0-1"},
			{Name: "vaultwarden", State: "exited", Image: "demo/vaultwarden:latest", ExitCode: 137},
			{Name: "jellyfin", State: "running", Health: "healthy", Image: "demo/jellyfin:latest"}, // no pin, never exited
		}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	require.Equal(t, "0-1", snap.Containers["minecraft"].Cpuset)
	require.Equal(t, 0, snap.Containers["minecraft"].ExitCode)
	require.Equal(t, 137, snap.Containers["vaultwarden"].ExitCode)
	require.Equal(t, "", snap.Containers["jellyfin"].Cpuset)
	require.Equal(t, 0, snap.Containers["jellyfin"].ExitCode)
}

// TestBuildSnapshotDropsSampleAtExactlyContainerFrameMaxAgeBoundary pins
// the per-sample freshness gate's own boundary (main.go: "nowUnix-
// sample.TS >= containerFrameMaxAge"): a sample exactly containerFrameMaxAge
// seconds old must be dropped, not just one older than that -- >=, not >.
func TestBuildSnapshotDropsSampleAtExactlyContainerFrameMaxAgeBoundary(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().Unix()
	st.Record(store.SeriesKey{Kind: "container", Entity: "db", Metric: "mem.limit_bytes"}, now-containerFrameMaxAge, 1e9) // exactly at the boundary
	st.Record(store.SeriesKey{Kind: "container", Entity: "db", Metric: "mem.bytes"}, now-(containerFrameMaxAge-1), 5e8)   // one second younger: must survive

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "db", State: "running"}}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	c, ok := snap.Containers["db"]
	require.True(t, ok)
	_, hasAtBoundary := c.Metrics["mem.limit_bytes"]
	require.False(t, hasAtBoundary, "a sample exactly containerFrameMaxAge seconds old must be dropped (>=, not >)")
	require.Equal(t, 5e8, c.Metrics["mem.bytes"], "a sample one second younger than the boundary must still come through")
}

// TestBuildSnapshotDropsStaleContainerGPUBusyPct pins main.go's own claim
// (buildSnapshot's per-sample freshness-gate comment) that the same gate
// covers container-attributed gpu.*.busy_pct going quiet, not just the
// mem/cpu-family metrics the other stale-sample test already exercises --
// gpu.render.busy_pct is one of the four names resourceMetrics' own "gpu"
// resource family recognizes (api_history.go).
func TestBuildSnapshotDropsStaleContainerGPUBusyPct(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().Unix()
	st.Record(store.SeriesKey{Kind: "container", Entity: "plex", Metric: "gpu.render.busy_pct"}, now-90, 42.0) // stale: older than containerFrameMaxAge

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "plex", State: "running"}}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil, nil)()

	c, ok := snap.Containers["plex"]
	require.True(t, ok)
	_, hasStale := c.Metrics["gpu.render.busy_pct"]
	require.False(t, hasStale, "a stale container-attributed gpu.render.busy_pct sample must be dropped, same as any other stale metric on a running container")
}

// TestBuildSnapshotMergesDiskMetaFromRealAndFake pins the disk_meta
// analogue of the fake-Metas test above: a real box's own unraid
// collector (ticked against a hand-written disks.ini) and fake mode's
// own DiskMeta overlay both land in snap.DiskMeta, keyed by their own
// disjoint slots, neither one clobbering the other.
func TestBuildSnapshotMergesDiskMetaFromRealAndFake(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	unraidDir := t.TempDir()
	// var.ini is Tick's one hard dependency (tickArray's own doc) -- a
	// minimal but valid one, so Tick actually reaches tickDisks below
	// rather than short-circuiting on a missing-file error.
	require.NoError(t, os.WriteFile(filepath.Join(unraidDir, "var.ini"), []byte(`version="7.3.2"
mdState="STARTED"
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(unraidDir, "disks.ini"), []byte(`["disk1"]
name="disk1"
device="sdc"
status="DISK_OK"
temp="31"
numErrors="0"
fsSize="0"
fsFree="0"
spundown="0"
rotational="1"
`), 0o644))

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, unraidDir, "/proc")
	require.NoError(t, ur.Tick(context.Background(), time.Unix(1000, 0)))
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }
	fakeDiskMeta := func() map[string]unraid.DiskMeta {
		return map[string]unraid.DiskMeta{"flash": {Device: "sdi", Kind: "usb"}}
	}

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, fakeDiskMeta, nil)()

	require.Equal(t, server.DiskMetaDTO{Device: "sdc", Kind: "hdd"}, snap.DiskMeta["disk1"], "the real unraid collector's own DiskMeta must survive into the DTO")
	require.Equal(t, server.DiskMetaDTO{Device: "sdi", Kind: "usb"}, snap.DiskMeta["flash"], "fake mode's own DiskMeta overlay must land alongside it, not replace it")
}

// TestBuildSnapshotAlertsBlockFiltersFiringJoinsRuleNameAndFlagsSilenced
// pins Task 8's frame assembly against a real store: a "pending"
// instance (engine bookkeeping, never user-facing) must not appear, a
// "firing" one must carry its rule's Name (alert_instances itself only
// stores rule_id) and Silenced when a covering silence exists.
func TestBuildSnapshotAlertsBlockFiltersFiringJoinsRuleNameAndFlagsSilenced(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	require.NoError(t, st.UpsertAlertRule(store.AlertRule{
		ID: "host-cpu-high", Name: "Host CPU high", Enabled: true, Builtin: true,
		Type: "threshold", Kind: "host", EntityGlob: "*", Metric: "cpu.total", Op: ">",
		Threshold: 85, Severity: "warning", UpdatedAt: 1000,
	}))
	if _, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: "host-cpu-high", Kind: "host", State: "firing", Severity: "warning",
		Value: 91, Threshold: 85, Summary: "host is at 91.0% (over 85.0% for 0s)", StartedAt: 900, FiredAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAlertInstance(store.AlertInstance{
		RuleID: "host-cpu-high", Kind: "host", Entity: "pending-entity", State: "pending",
		Severity: "warning", Value: 86, Threshold: 85, StartedAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSilence(store.Silence{RuleID: "host-cpu-high", Until: time.Now().Unix() + 3600, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, nil, nil)()

	require.Len(t, snap.Alerts.Firing, 1, "the pending instance must be excluded from the frame")
	f := snap.Alerts.Firing[0]
	require.Equal(t, "host-cpu-high", f.RuleID)
	require.Equal(t, "Host CPU high", f.RuleName, "the frame must join the rule's Name, not just carry rule_id")
	require.Equal(t, 91.0, f.Value)
	require.Equal(t, "host is at 91.0% (over 85.0% for 0s)", f.Summary, "the instance's own Summary must carry through -- the Alerts view's event-alert detail line reads this")
	require.True(t, f.Silenced, "the rule-wide silence (entity \"\") must cover this instance")
	require.Equal(t, 1, snap.Alerts.FiringCount)
	require.Equal(t, 0, snap.Alerts.Truncated)
}

// TestBuildSnapshotAlertsBlockCapsAtTwentyAndReportsTruncated pins the
// frame's own noise guard: a pathological rule cannot bloat every 2s
// frame for every connected client.
func TestBuildSnapshotAlertsBlockCapsAtTwentyAndReportsTruncated(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	require.NoError(t, st.UpsertAlertRule(store.AlertRule{
		ID: "container-mem-limit-high", Name: "Container memory limit high", Enabled: true,
		Type: "threshold", Kind: "container", EntityGlob: "*", Metric: "mem.limit_pct", Op: ">",
		Threshold: 85, Severity: "warning", UpdatedAt: 1000,
	}))
	for i := 0; i < 25; i++ {
		if _, err := st.UpsertAlertInstance(store.AlertInstance{
			RuleID: "container-mem-limit-high", Kind: "container", Entity: fmt.Sprintf("c%d", i),
			State: "firing", Severity: "warning", Value: 90, Threshold: 85, StartedAt: 900, FiredAt: 1000,
		}); err != nil {
			t.Fatal(err)
		}
	}

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, nil, nil)()

	require.Len(t, snap.Alerts.Firing, server.AlertsFrameCap)
	require.Equal(t, 25, snap.Alerts.FiringCount)
	require.Equal(t, 5, snap.Alerts.Truncated)
}

// TestBuildSnapshotAlertsBlockEmptyChannelsWhenNoDispatcher pins the
// nil-dispatcher degradation: main wiring always passes a real one, but
// a test (or a future caller) that doesn't must get an empty map, never
// a panic.
func TestBuildSnapshotAlertsBlockEmptyChannelsWhenNoDispatcher(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{} }

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, nil, nil)()

	require.Empty(t, snap.Alerts.Channels)
	require.Empty(t, snap.Alerts.Firing)
}

// TestChannelHealthMapReportsEveryConfiguredChannel pins the small
// shared helper both alertsAdapter.Channels (GET /api/alerts) and the
// frame block call into.
func TestChannelHealthMapReportsEveryConfiguredChannel(t *testing.T) {
	notifyDir := t.TempDir()
	d := alert.NewDispatcher(nil, []alert.Channel{alert.NewNotifyChannel(notifyDir, nil, nil)}, nil, nil)
	require.Equal(t, map[string]string{"notify": "ok"}, channelHealthMap(d))
	require.Empty(t, channelHealthMap(nil))
}

// TestBuildContainersListMergesFakeMetas pins the same fix for
// /api/containers: the fake fleet must be listed the same way a real
// running fleet would be, not just present in the live snapshot.
func TestBuildContainersListMergesFakeMetas(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "frigate", State: "running", Health: "healthy", Image: "demo/frigate:latest"}}
	}

	list := buildContainersList(dc, fakeMetas)()

	require.Len(t, list, 1)
	require.Equal(t, "frigate", list[0].Name)
	require.Equal(t, "running", list[0].State)
	require.Equal(t, "demo/frigate:latest", list[0].Image)
}

// TestBuildContainersListNilFakeMetasUnaffected pins real-mode
// behavior (GANTRY_FAKE_DATA unset): a nil fakeMetas must not change
// buildContainersList's existing dc.Running()-only contract.
func TestBuildContainersListNilFakeMetasUnaffected(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")

	require.Empty(t, buildContainersList(dc, nil)())
}

// TestBuildFleetMergesFakeMetasAndSeesAllStates pins buildFleet's two
// departures from buildContainersList: it merges fakeMetas the same
// unconditional way, but sources from dc.All() rather than dc.Running(),
// so the alert engine's boot seeding can see a container's real state
// (not just "must be running, this list said so").
func TestBuildFleetMergesFakeMetasAndSeesAllStates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{Name: "sonarr", State: "exited", Health: "unhealthy"}}
	}

	fleet := buildFleet(dc, fakeMetas)()

	require.Equal(t, []alert.FleetMember{{Name: "sonarr", State: "exited", Health: "unhealthy"}}, fleet)
}

// TestBuildFleetNilFakeMetasUnaffected pins real-mode behavior
// (GANTRY_FAKE_DATA unset): a nil fakeMetas must not change buildFleet's
// existing dc.All()-only contract.
func TestBuildFleetNilFakeMetasUnaffected(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")

	require.Empty(t, buildFleet(dc, nil)())
}

// TestBuildClassOfOnlyResolvesDiskKind pins the kind gate itself (every
// other kind has no notion of class yet, so it must read as "" without
// even consulting DiskMeta) -- disk1 also comes back "" here because no
// var.ini/disks.ini exist under this bare temp dir for ur to have ever
// ticked, which is exactly the "absent classification" MatchClass's own
// negation semantics are written to tolerate.
func TestBuildClassOfOnlyResolvesDiskKind(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ur := unraid.New(st, st, t.TempDir(), "/proc")

	classOf := buildClassOf(ur)

	require.Equal(t, "", classOf("host", "disk1"), "non-disk kind never consults DiskMeta")
	require.Equal(t, "", classOf("disk", "disk1"), "unclassified disk (no ini ticked yet) reads as unknown, not a crash")
}

// fakeMeta builds a minimal known-container answer for a lookupByName
// stand-in, without needing a real docker.Collector/daemon.
func fakeMeta(name string) docker.Meta { return docker.Meta{Name: name} }

// TestContainerStorageResolvesMountsAndDeviceIO pins containerStorage's
// full happy path: a hand-built lookupMeta/poolSlots pair (no real
// docker.Collector registry or daemon needed) plus a bare
// *store.Live carrying this container's live:io.* samples -- proving the
// mount->storage resolution and the per-device rate assembly both land
// in the DTO correctly.
func TestContainerStorageResolvesMountsAndDeviceIO(t *testing.T) {
	lookupMeta := func(name string) (docker.Meta, bool) {
		if name != "jellyfin" {
			return docker.Meta{}, false
		}
		return docker.Meta{
			Name: "jellyfin",
			Mounts: []docker.MountInfo{
				{Source: "/mnt/user/appdata/jellyfin", Destination: "/config", RW: true},
				{Source: "/mnt/cache/transcode", Destination: "/tmp", RW: true},
			},
		}, true
	}
	poolSlots := func() []string { return []string{"cache"} }
	noDiskMeta := func() map[string]unraid.DiskMeta { return nil }
	noSharePlacement := func() map[string]unraid.SharePlacement { return nil }

	live := store.NewLive(8)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sda.read_bps"}, 1000, 123.5)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sda.write_bps"}, 1000, 45)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "cpu.pct"}, 1000, 4.2) // must not leak into devices

	dto, ok := containerStorage(lookupMeta, poolSlots, noDiskMeta, nil, noSharePlacement, nil, nil, "/unused", live, "jellyfin", 1000)
	require.True(t, ok)

	require.Equal(t, []server.MountDTO{
		{Source: "/mnt/user/appdata/jellyfin", Destination: "/config", RW: true, Storage: server.StorageRefDTO{Kind: "share", Name: "appdata"}},
		{Source: "/mnt/cache/transcode", Destination: "/tmp", RW: true, Storage: server.StorageRefDTO{Kind: "pool", Name: "cache"}},
	}, dto.Mounts)
	require.Equal(t, []server.DeviceIODTO{{Device: "sda", Label: "sda", ReadBps: 123.5, WriteBps: 45}}, dto.Devices)
}

// TestContainerStorageJoinsSharePlacementRealAndFakeOverlay pins the new
// share->cache-pool join: a share sharePlacement (the real collector's
// own SharePlacement()) knows about gets it straight through, a share
// only fakeSharePlacement covers still resolves (fake-data mode has no
// real shares.ini at all), and a mount that isn't kind=share never gets
// a Placement even if the exact same name happened to appear in the map
// (Placement is share-only, by construction, not merely by absence).
func TestContainerStorageJoinsSharePlacementRealAndFakeOverlay(t *testing.T) {
	lookupMeta := func(name string) (docker.Meta, bool) {
		return docker.Meta{
			Name: name,
			Mounts: []docker.MountInfo{
				{Source: "/mnt/user/appdata/" + name, Destination: "/config", RW: true},
				{Source: "/mnt/user/downloads", Destination: "/downloads", RW: true},
				{Source: "/mnt/cache/appdata", Destination: "/tmp", RW: true}, // pool, not share -- name collides with the share on purpose
			},
		}, true
	}
	poolSlots := func() []string { return []string{"cache"} }
	noDiskMeta := func() map[string]unraid.DiskMeta { return nil }
	sharePlacement := func() map[string]unraid.SharePlacement {
		return map[string]unraid.SharePlacement{"appdata": {Mode: "yes", Pool: "cache"}}
	}
	fakeSharePlacement := func() map[string]unraid.SharePlacement {
		return map[string]unraid.SharePlacement{"downloads": {Mode: "only", Pool: "rocket_pool"}}
	}

	dto, ok := containerStorage(lookupMeta, poolSlots, noDiskMeta, nil, sharePlacement, fakeSharePlacement, nil, "/unused", store.NewLive(8), "jellyfin", 1000)
	require.True(t, ok)

	byDest := map[string]server.MountDTO{}
	for _, m := range dto.Mounts {
		byDest[m.Destination] = m
	}
	require.Equal(t, &server.SharePlacementDTO{Mode: "yes", Pool: "cache"}, byDest["/config"].Storage.Placement)
	require.Equal(t, &server.SharePlacementDTO{Mode: "only", Pool: "rocket_pool"}, byDest["/downloads"].Storage.Placement)
	require.Nil(t, byDest["/tmp"].Storage.Placement, "kind=pool never gets a share Placement, even sharing a name with a real share")
}

// TestContainerStorageResolvesDeviceLabelsViaDiskMetaJoinAndFakeOverlay
// pins the label-resolution wiring containerStorage adds on top of
// deviceIOFromSamples' bare device rows: a device diskMeta places in a
// known slot picks up that slot's name (the real-collector path, sysRoot
// unused for it), a device fakeDiskMeta adds joins the SAME way (proving
// the real+fake merge actually happens before the join, not after), and
// a device fakeDeviceLabels names directly wins outright even though
// diskMeta/sysRoot would have resolved (or failed to resolve) it some
// other way -- the one override this collector's own DiskMeta join can
// never produce on its own (see fake.Generator.DeviceLabels' own doc).
func TestContainerStorageResolvesDeviceLabelsViaDiskMetaJoinAndFakeOverlay(t *testing.T) {
	lookupMeta := func(string) (docker.Meta, bool) { return docker.Meta{Name: "jellyfin"}, true }
	poolSlots := func() []string { return nil }
	diskMeta := func() map[string]unraid.DiskMeta {
		return map[string]unraid.DiskMeta{"disk1": {Device: "sdc", Kind: "hdd"}}
	}
	fakeDiskMeta := func() map[string]unraid.DiskMeta {
		return map[string]unraid.DiskMeta{"rocket_pool": {Device: "nvme0n1", Kind: "nvme"}}
	}
	fakeDeviceLabels := func() map[string]unraid.DeviceLabel {
		return map[string]unraid.DeviceLabel{"loop2": {Label: "docker.img"}}
	}

	live := store.NewLive(8)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sdc.read_bps"}, 1000, 1)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.nvme0n1.read_bps"}, 1000, 2)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.loop2.read_bps"}, 1000, 3)

	noSharePlacement := func() map[string]unraid.SharePlacement { return nil }
	dto, ok := containerStorage(lookupMeta, poolSlots, diskMeta, fakeDiskMeta, noSharePlacement, nil, fakeDeviceLabels, "/unused", live, "jellyfin", 1000)
	require.True(t, ok)

	byDevice := map[string]server.DeviceIODTO{}
	for _, d := range dto.Devices {
		byDevice[d.Device] = d
	}
	require.Equal(t, "disk1", byDevice["sdc"].Label, "the real collector's own diskMeta must still join")
	require.Equal(t, "hdd", byDevice["sdc"].Kind)
	require.Equal(t, "rocket_pool", byDevice["nvme0n1"].Label, "fakeDiskMeta must merge into the SAME join, not bypass it")
	require.Equal(t, "nvme", byDevice["nvme0n1"].Kind)
	require.Equal(t, "docker.img", byDevice["loop2"].Label, "fakeDeviceLabels overrides outright for a device the join can't cover")
	require.Equal(t, "", byDevice["loop2"].Kind)
}

// TestContainerStorageThreadsSysRootIntoDeviceLabelResolution pins that
// containerStorage's sysRoot parameter is the one ResolveDeviceLabel's
// loop branch actually reads -- a real (non-fake) loop device resolves
// its friendly label only when sysRoot points at the fixture tree
// carrying its backing_file.
func TestContainerStorageThreadsSysRootIntoDeviceLabelResolution(t *testing.T) {
	sysRoot := t.TempDir()
	// Mirrors a real /sys/block/loop0/loop/backing_file -- see unraid.
	// ResolveDeviceLabel's own doc for the exact path this reads.
	loopDir := filepath.Join(sysRoot, "block", "loop0", "loop")
	require.NoError(t, os.MkdirAll(loopDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(loopDir, "backing_file"), []byte("/mnt/user/system/docker/docker.img\n"), 0o644))

	lookupMeta := func(string) (docker.Meta, bool) { return docker.Meta{Name: "jellyfin"}, true }
	poolSlots := func() []string { return nil }
	noDiskMeta := func() map[string]unraid.DiskMeta { return nil }
	noSharePlacement := func() map[string]unraid.SharePlacement { return nil }

	live := store.NewLive(8)
	live.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.loop0.read_bps"}, 1000, 1)

	dto, ok := containerStorage(lookupMeta, poolSlots, noDiskMeta, nil, noSharePlacement, nil, nil, sysRoot, live, "jellyfin", 1000)
	require.True(t, ok)
	require.Equal(t, "docker.img", dto.Devices[0].Label)
}

// TestContainerStorageUnknownContainerReturnsFalse pins the not-found
// path: lookupMeta reporting false must surface as ok=false, exactly
// docker.Collector.LookupByName's own shape.
func TestContainerStorageUnknownContainerReturnsFalse(t *testing.T) {
	lookupMeta := func(string) (docker.Meta, bool) { return docker.Meta{}, false }
	poolSlots := func() []string { return nil }
	noDiskMeta := func() map[string]unraid.DiskMeta { return nil }
	noSharePlacement := func() map[string]unraid.SharePlacement { return nil }

	_, ok := containerStorage(lookupMeta, poolSlots, noDiskMeta, nil, noSharePlacement, nil, nil, "/unused", store.NewLive(8), "ghost", 0)
	require.False(t, ok)
}

// TestContainerStorageEmptyMountsAndDevicesAreNonNilSlices pins the
// nil-vs-empty shape a bare container (no mounts, no live IO samples
// yet) must produce: [] in the wire JSON, not null -- see
// StorageDTO's own doc on why the server package cares about this.
func TestContainerStorageEmptyMountsAndDevicesAreNonNilSlices(t *testing.T) {
	lookupMeta := func(string) (docker.Meta, bool) { return docker.Meta{Name: "bare"}, true }
	poolSlots := func() []string { return nil }
	noDiskMeta := func() map[string]unraid.DiskMeta { return nil }
	noSharePlacement := func() map[string]unraid.SharePlacement { return nil }

	dto, ok := containerStorage(lookupMeta, poolSlots, noDiskMeta, nil, noSharePlacement, nil, nil, "/unused", store.NewLive(8), "bare", 0)
	require.True(t, ok)
	require.NotNil(t, dto.Mounts)
	require.Empty(t, dto.Mounts)
	require.NotNil(t, dto.Devices)
	require.Empty(t, dto.Devices)
}

// TestDeviceIOFromSamplesCombinesReadAndWriteZeroFillingTheMissingHalf
// pins deviceIOFromSamples' per-device assembly directly: two devices,
// one with both rates, one with only a write sample (its read RateTracker
// key hasn't produced a second reading yet -- see cgroupv2.go) -- the
// latter must still appear, with ReadBps left at its zero value rather
// than being dropped, and results come back sorted by device name.
func TestDeviceIOFromSamplesCombinesReadAndWriteZeroFillingTheMissingHalf(t *testing.T) {
	samples := map[string]store.Sample{
		"live:io.sdb.write_bps": {TS: 100, Val: 10},
		"live:io.sda.read_bps":  {TS: 100, Val: 20},
		"live:io.sda.write_bps": {TS: 100, Val: 30},
	}

	got := deviceIOFromSamples(samples, 100)

	require.Equal(t, []server.DeviceIODTO{
		{Device: "sda", ReadBps: 20, WriteBps: 30},
		{Device: "sdb", ReadBps: 0, WriteBps: 10},
	}, got)
}

func TestDeviceIOFromSamplesEmptyWhenNoSamples(t *testing.T) {
	got := deviceIOFromSamples(map[string]store.Sample{}, 100)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// TestDeviceIOFromSamplesExcludesStaleSamples pins the stale-sample age cutoff.
func TestDeviceIOFromSamplesExcludesStaleSamples(t *testing.T) {
	now := int64(1000)
	samples := map[string]store.Sample{
		"live:io.sda.read_bps": {TS: now - containerFrameMaxAge, Val: 999},
	}

	got := deviceIOFromSamples(samples, now)

	require.Empty(t, got, "a sample containerFrameMaxAge seconds old or older must not surface as a live device row")
}

// TestDeviceIOFromSamplesUnknownSuffixProducesNoRow pins that an unknown suffix fabricates no row.
func TestDeviceIOFromSamplesUnknownSuffixProducesNoRow(t *testing.T) {
	samples := map[string]store.Sample{
		"live:io.sda.iops": {TS: 100, Val: 42},
	}

	got := deviceIOFromSamples(samples, 100)

	require.Empty(t, got, "an unrecognized suffix must not fabricate a device row")
}

// TestBuildContainerStorageUnknownReturnsFalse is a thin wiring check
// for buildContainerStorage itself (as opposed to containerStorage's
// pure logic, exercised above): a real, never-ticked docker.Collector's
// registry knows no names at all, so this proves the closure is wired
// to dc.LookupByName/ur.Slots/st.Live() correctly without needing a
// live daemon to populate a Meta.
func TestBuildContainerStorageUnknownReturnsFalse(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")

	_, ok := buildContainerStorage(dc, ur, st, nil, nil, nil, nil, "/unused")("ghost")
	require.False(t, ok)
}

// TestBuildContainerStorageMergesFakeMetas pins the fix-round fix
// (finding 1): fake-data mode's synthetic containers never touch dc's
// registry (same reason buildContainersList/buildSnapshot each merge
// fakeMetas), so without merging it here too, every fake container's
// /storage route 404s -- lookupMeta must fall back to fakeMetas' entries
// when dc's registry doesn't know the name.
func TestBuildContainerStorageMergesFakeMetas(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")
	fakeMetas := func() []docker.Meta {
		return []docker.Meta{{
			Name:   "frigate",
			Mounts: []docker.MountInfo{{Source: "/mnt/user/appdata/frigate", Destination: "/config", RW: true}},
		}}
	}

	dto, ok := buildContainerStorage(dc, ur, st, fakeMetas, nil, nil, nil, "/unused")("frigate")

	require.True(t, ok, "a fake-mode container must resolve via fakeMetas, not 404")
	require.Equal(t, []server.MountDTO{
		{Source: "/mnt/user/appdata/frigate", Destination: "/config", RW: true, Storage: server.StorageRefDTO{Kind: "share", Name: "appdata"}},
	}, dto.Mounts)
}

// TestBuildContainerStorageNilFakeMetasUnaffected pins real-mode
// behavior (GANTRY_FAKE_DATA unset): a nil fakeMetas must not change
// buildContainerStorage's existing dc.LookupByName-only contract.
func TestBuildContainerStorageNilFakeMetasUnaffected(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")

	_, ok := buildContainerStorage(dc, ur, st, nil, nil, nil, nil, "/unused")("ghost")
	require.False(t, ok)
}

func TestHealthcheckExitPath(t *testing.T) {
	port := freePort(t)
	// Nothing listening → healthcheck must report failure.
	err := healthcheck(func(k string) string {
		if k == "GANTRY_PORT" {
			return fmt.Sprint(port)
		}
		return ""
	})
	require.Error(t, err)
}

func TestRunReturnsOnBindFailure(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }() // hold the port for the whole test so run()'s bind fails
	port := l.Addr().(*net.TCPAddr).Port

	env := map[string]string{
		"GANTRY_PORT":    fmt.Sprint(port),
		"GANTRY_DB_PATH": filepath.Join(t.TempDir(), "g.db"),
	}
	done := make(chan error, 1)
	go func() { done <- run(context.Background(), func(k string) string { return env[k] }, "test-ver") }()

	select {
	case err := <-done:
		require.Error(t, err, "run must return the bind error")
	case <-time.After(5 * time.Second):
		t.Fatal("run() hung on bind failure instead of returning the error")
	}
}

// TestRunSeedsDefaultAlertRulesAtBoot pins main.go's wiring of
// store.SeedAlertRules(store.DefaultAlertRules()) into run(): after a
// full boot and graceful shutdown, the on-disk DB carries all twelve
// builtin rules. Re-opening the same db path only after run() has fully
// returned (rather than querying through some new endpoint) avoids
// racing the single-writer handle run() itself still owns while live --
// there is no /api/alerts/* route yet (Task 8), so this is the only way
// to observe main's seed call at all.
func TestRunSeedsDefaultAlertRulesAtBoot(t *testing.T) {
	port := freePort(t)
	dbPath := filepath.Join(t.TempDir(), "g.db")
	env := map[string]string{
		"GANTRY_PORT":    fmt.Sprint(port),
		"GANTRY_DB_PATH": dbPath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down")
	}

	st, err := store.Open(dbPath, nil)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	rules, err := st.AlertRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 12, "main.go must seed all twelve default alert rules at boot")
	for _, r := range rules {
		require.True(t, r.Builtin)
		require.True(t, r.Enabled)
	}
}

// TestWireDockerCollectorPinsHostCoresToHostCollector pins main.go's own
// dc.HostCores wiring: it must be the host collector's own NumCPU method
// (the /proc/stat-derived, cpuset-immune count), not some other int-
// returning func (e.g. runtime.NumCPU directly) that would happen to
// satisfy the field's type and pass every other test in the suite.
// Method values of the same method share one underlying function per
// (type, method) pair regardless of receiver, so comparing the
// reflect.Value's Pointer() -- not the bound values themselves, which
// Go doesn't let you compare at all -- correctly distinguishes "some
// *host.Collector's NumCPU" from any other func() int.
func TestWireDockerCollectorPinsHostCoresToHostCollector(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	h := host.New(st, "/proc", "/host/sys")
	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")

	wireDockerCollector(dc, h, "/host/sys/fs/cgroup")

	require.Equal(t, reflect.ValueOf(h.NumCPU).Pointer(), reflect.ValueOf(dc.HostCores).Pointer(),
		"dc.HostCores must be wired to the host collector's own NumCPU")
}

// --- webhook target settings-blob adapter -----------------------------------

func newAlertTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestLoadWebhookTargetsEmptyWhenNeverSet(t *testing.T) {
	st := newAlertTestStore(t)
	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Empty(t, targets)
}

func TestSaveAndLoadWebhookTargetsRoundTrip(t *testing.T) {
	st := newAlertTestStore(t)
	want := []alert.WebhookTarget{
		{ID: "home", Name: "Home Assistant", URL: "https://ha.local/hook", Enabled: true, TimeoutS: 10},
		{ID: "ntfy", Name: "ntfy", URL: "https://ntfy.sh/gantry", Enabled: false, TimeoutS: 5},
	}
	require.NoError(t, saveWebhookTargets(st, want))

	got, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSeedWebhookTargetFromEnvNoopWhenEnvEmpty(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, seedWebhookTargetFromEnv(st, ""))
	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Empty(t, targets)
}

// TestSeedWebhookTargetFromEnvRemovesEnvTargetWhenCleared pins the
// unset direction: the env var is the source of truth for the "env"
// target in BOTH directions, so clearing GANTRY_WEBHOOK_URL and
// rebooting must remove the target a previous boot created -- not
// leave it silently delivering to a URL the operator believes is gone.
func TestSeedWebhookTargetFromEnvRemovesEnvTargetWhenCleared(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, seedWebhookTargetFromEnv(st, "https://example.com/hook"))
	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 1)

	require.NoError(t, seedWebhookTargetFromEnv(st, ""))
	targets, err = loadWebhookTargets(st)
	require.NoError(t, err)
	require.Empty(t, targets, "clearing the env var must remove the env target on the next boot")
}

func TestSeedWebhookTargetFromEnvClearingPreservesOtherTargets(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, saveWebhookTargets(st, []alert.WebhookTarget{
		{ID: "home", Name: "Home Assistant", URL: "https://ha.local/hook", Enabled: true, TimeoutS: 10},
	}))
	require.NoError(t, seedWebhookTargetFromEnv(st, "https://example.com/hook"))
	require.NoError(t, seedWebhookTargetFromEnv(st, ""))

	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 1, "only the env target goes; hand-configured targets stay")
	require.Equal(t, "home", targets[0].ID)
}

func TestSeedWebhookTargetFromEnvInsertsOnFirstBoot(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, seedWebhookTargetFromEnv(st, "https://example.com/hook"))

	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "env", targets[0].ID)
	require.Equal(t, "https://example.com/hook", targets[0].URL)
	require.True(t, targets[0].Enabled)
	require.Equal(t, 10, targets[0].TimeoutS)
}

func TestSeedWebhookTargetFromEnvSyncsURLOnLaterBoots(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, seedWebhookTargetFromEnv(st, "https://old.example.com/hook"))
	require.NoError(t, seedWebhookTargetFromEnv(st, "https://new.example.com/hook"))

	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 1, "re-seeding must update the existing env target, not add a second one")
	require.Equal(t, "https://new.example.com/hook", targets[0].URL)
}

func TestSeedWebhookTargetFromEnvPreservesOtherTargets(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, saveWebhookTargets(st, []alert.WebhookTarget{
		{ID: "home", Name: "Home Assistant", URL: "https://ha.local/hook", Enabled: true, TimeoutS: 10},
	}))
	require.NoError(t, seedWebhookTargetFromEnv(st, "https://example.com/hook"))

	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 2)
	ids := map[string]bool{}
	for _, tgt := range targets {
		ids[tgt.ID] = true
	}
	require.True(t, ids["home"])
	require.True(t, ids["env"])
}

func TestBuildWebhookChannelsSkipsDisabledAndInvalid(t *testing.T) {
	targets := []alert.WebhookTarget{
		{ID: "ok", URL: "https://example.com/hook", Enabled: true, TimeoutS: 10},
		{ID: "disabled", URL: "https://example.com/hook", Enabled: false, TimeoutS: 10},
		{ID: "bad-scheme", URL: "file:///etc/passwd", Enabled: true, TimeoutS: 10},
	}
	channels := buildWebhookChannels(targets, "v-test", time.Now)
	require.Len(t, channels, 1)
	require.Equal(t, "webhook:ok", channels[0].ID())
}

// --- dispatcher wiring --------------------------------------------------------

func TestBuildDispatcherIncludesNotifyAndConfiguredWebhookChannels(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, saveWebhookTargets(st, []alert.WebhookTarget{
		{ID: "home", URL: "https://example.com/hook", Enabled: true, TimeoutS: 10},
	}))
	cfg := config.New(st, func(string) string { return "" })

	d, err := buildDispatcher(st, cfg, func(string) string { return "" }, "v-test", false)
	require.NoError(t, err)
	t.Cleanup(d.Stop)

	ids := make([]string, len(d.Channels))
	for i, ch := range d.Channels {
		ids[i] = ch.ID()
	}
	require.Contains(t, ids, "notify")
	require.Contains(t, ids, "webhook:home")
}

// TestAlertEngineFiresThroughDispatcherToNotifySpool wires the real
// buildDispatcher output straight into a real alert.Engine -- the actual
// production wiring code, not a re-implementation of it -- and drives
// one Tick through an event rule (no ring/sustained-for window to seed,
// unlike a threshold rule) to prove a fired alert actually reaches a
// file in the notify spool end to end.
func TestAlertEngineFiresThroughDispatcherToNotifySpool(t *testing.T) {
	st := newAlertTestStore(t)
	notifyDir := t.TempDir()
	getenv := func(k string) string {
		if k == "GANTRY_NOTIFY_DIR" {
			return notifyDir
		}
		return ""
	}
	cfg := config.New(st, getenv)

	dispatcher, err := buildDispatcher(st, cfg, getenv, "v-test", false)
	require.NoError(t, err)
	t.Cleanup(dispatcher.Stop)

	now := time.Now()
	require.NoError(t, st.UpsertAlertRule(store.AlertRule{
		ID: "test-event-rule", Name: "Test event rule", Enabled: true, Type: "event",
		EntityGlob: "*", EventKinds: "test.event", Severity: "warning", UpdatedAt: now.Unix(),
	}))

	noMatch := func(string, string, int64) (map[string][]store.Sample, map[string]int64) { return nil, nil }
	noClass := func(string, string) string { return "" }
	eng := alert.New(st, noMatch, noClass, nil, dispatcher.Dispatch, func() time.Time { return now })

	// The engine's cursor clamps to MaxEventID on its own first Tick (so
	// a restart never replays the whole events table as fresh alerts) --
	// an event appended before that first Tick is treated as pre-
	// existing, not new. One "nothing to see yet" tick first establishes
	// the cursor, exactly like a real boot would.
	require.NoError(t, eng.Tick(context.Background()))

	_, err = st.AppendEvent(store.Event{Kind: "test.event", Entity: "widget1", Severity: "warning", Detail: "boom"})
	require.NoError(t, err)

	require.NoError(t, eng.Tick(context.Background()))

	require.Eventually(t, func() bool {
		entries, derr := os.ReadDir(filepath.Join(notifyDir, "unread"))
		return derr == nil && len(entries) == 1
	}, 2*time.Second, 20*time.Millisecond)

	entries, err := os.ReadDir(filepath.Join(notifyDir, "unread"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	body, err := os.ReadFile(filepath.Join(notifyDir, "unread", entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(body), "widget1")
}

// TestRunSeedsWebhookTargetFromEnvAtBoot mirrors
// TestRunSeedsDefaultAlertRulesAtBoot: after a full boot and graceful
// shutdown with GANTRY_WEBHOOK_URL set, the on-disk settings table
// carries the "env" target -- re-opened only after run() has fully
// returned, for the same single-writer-handle reason that test documents.
func TestRunSeedsWebhookTargetFromEnvAtBoot(t *testing.T) {
	port := freePort(t)
	dbPath := filepath.Join(t.TempDir(), "g.db")
	env := map[string]string{
		"GANTRY_PORT":        fmt.Sprint(port),
		"GANTRY_DB_PATH":     dbPath,
		"GANTRY_WEBHOOK_URL": "https://example.com/gantry-hook",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/healthz", port))
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down")
	}

	st, err := store.Open(dbPath, nil)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "env", targets[0].ID)
	require.Equal(t, "https://example.com/gantry-hook", targets[0].URL)
}

// TestRunWiresAlertsAndWebhooksAPIEndToEnd is Task 8's own end-to-end
// pin: unlike api_alerts_test.go's handler tests (which exercise the
// route logic against a fake AlertsIface/WebhooksIface), this drives the
// REAL alertsAdapter/webhooksAdapter through a live run() -- the seeded
// twelve builtins must be visible over HTTP, the frame must carry a
// non-nil (if empty) alerts block, and the GANTRY_WEBHOOK_URL-seeded
// "env" target must read back through GET /api/alerts/webhooks as
// env_overridden with its header never present.
func TestRunWiresAlertsAndWebhooksAPIEndToEnd(t *testing.T) {
	port := freePort(t)
	dbPath := filepath.Join(t.TempDir(), "g.db")
	env := map[string]string{
		"GANTRY_PORT":        fmt.Sprint(port),
		"GANTRY_DB_PATH":     dbPath,
		"GANTRY_WEBHOOK_URL": "https://example.com/gantry-hook",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("run did not shut down")
		}
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/api/healthz")
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	rulesResp, err := http.Get(base + "/api/alerts/rules")
	require.NoError(t, err)
	defer drainAndClose(rulesResp)
	require.Equal(t, http.StatusOK, rulesResp.StatusCode)
	var rulesBody struct {
		Rules []server.AlertRuleDTO `json:"rules"`
	}
	require.NoError(t, json.NewDecoder(rulesResp.Body).Decode(&rulesBody))
	require.Len(t, rulesBody.Rules, 12, "the seeded builtins must be visible through the real adapter over HTTP")

	alertsResp, err := http.Get(base + "/api/alerts")
	require.NoError(t, err)
	defer drainAndClose(alertsResp)
	require.Equal(t, http.StatusOK, alertsResp.StatusCode)
	var alertsBody struct {
		Active   []server.AlertInstanceDTO `json:"active"`
		Silences []server.SilenceDTO       `json:"silences"`
		Channels map[string]string         `json:"channels"`
	}
	require.NoError(t, json.NewDecoder(alertsResp.Body).Decode(&alertsBody))
	require.Empty(t, alertsBody.Active, "a fresh box has nothing firing yet")
	require.Contains(t, alertsBody.Channels, "notify", "the notify-spool channel is always wired")

	whResp, err := http.Get(base + "/api/alerts/webhooks")
	require.NoError(t, err)
	defer drainAndClose(whResp)
	require.Equal(t, http.StatusOK, whResp.StatusCode)
	raw, err := io.ReadAll(whResp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "header_value", "GET must never carry the secret field at all")
	var whBody struct {
		Targets []server.WebhookTargetDTO `json:"targets"`
	}
	require.NoError(t, json.Unmarshal(raw, &whBody))
	require.Len(t, whBody.Targets, 1)
	require.Equal(t, "env", whBody.Targets[0].ID)
	require.True(t, whBody.Targets[0].EnvOverridden)

	snapResp, err := http.Get(base + "/api/live/snapshot")
	require.NoError(t, err)
	defer drainAndClose(snapResp)
	var snap server.SnapshotDTO
	require.NoError(t, json.NewDecoder(snapResp.Body).Decode(&snap))
	require.NotNil(t, snap.Alerts.Firing, "the frame's alerts block must always be a real (if empty) array")
	require.Contains(t, snap.Alerts.Channels, "notify")
}

// --- Task 9: fake-mode alert demo -------------------------------------------

func TestResolveNotifyDirEnvAlwaysWinsRegardlessOfMode(t *testing.T) {
	for _, fakeMode := range []bool{false, true} {
		dir, err := resolveNotifyDir(func(k string) string {
			if k == "GANTRY_NOTIFY_DIR" {
				return "/custom/notify"
			}
			return ""
		}, fakeMode)
		require.NoError(t, err)
		require.Equal(t, "/custom/notify", dir, "fakeMode=%v", fakeMode)
	}
}

func TestResolveNotifyDirRealModeDefaultsToNotifyMount(t *testing.T) {
	dir, err := resolveNotifyDir(func(string) string { return "" }, false)
	require.NoError(t, err)
	require.Equal(t, "/notify", dir)
}

// TestResolveNotifyDirFakeModeCreatesFreshWritableTempDir pins Task 9's
// own contract: with no override, fake mode gets a real, distinct,
// writable directory each call -- never the unmounted "/notify" real
// mode falls back to -- so the notify channel's own construction-time
// probe (channel_notify.go's Health doc) finds it writable and reports
// "ok" immediately, with no operator action.
func TestResolveNotifyDirFakeModeCreatesFreshWritableTempDir(t *testing.T) {
	dirA, err := resolveNotifyDir(func(string) string { return "" }, true)
	require.NoError(t, err)
	require.NotEqual(t, "/notify", dirA)
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "probe"), []byte("x"), 0o644))

	dirB, err := resolveNotifyDir(func(string) string { return "" }, true)
	require.NoError(t, err)
	require.NotEqual(t, dirA, dirB, "each call with no override must get its own fresh directory")
}

func TestSeedFakeWebhookTargetsInsertsBothAndIsIdempotent(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, seedFakeWebhookTargets(st, 8380))

	targets, err := loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 2)
	byID := map[string]alert.WebhookTarget{}
	for _, tgt := range targets {
		byID[tgt.ID] = tgt
	}
	require.Equal(t, "http://127.0.0.1:8380/api/healthz", byID[fakeWebhookOKTargetID].URL)
	require.True(t, byID[fakeWebhookOKTargetID].Enabled)
	require.Equal(t, "http://127.0.0.1:1/dead", byID[fakeWebhookFailTargetID].URL)
	require.True(t, byID[fakeWebhookFailTargetID].Enabled)

	// Idempotent: a second call (a later boot) must not duplicate either
	// target, and must leave an in-between hand edit alone.
	edited := byID[fakeWebhookOKTargetID]
	edited.Enabled = false
	require.NoError(t, saveWebhookTargets(st, []alert.WebhookTarget{edited, byID[fakeWebhookFailTargetID]}))
	require.NoError(t, seedFakeWebhookTargets(st, 8380))

	targets, err = loadWebhookTargets(st)
	require.NoError(t, err)
	require.Len(t, targets, 2, "re-seeding must not duplicate either target")
	for _, tgt := range targets {
		if tgt.ID == fakeWebhookOKTargetID {
			require.False(t, tgt.Enabled, "re-seeding must not resurrect a hand-edited target")
		}
	}
}

// TestRunFakeModeSeedsFastRulesAndDemoWebhookTargets boots the real
// server with GANTRY_FAKE_DATA=1 and no GANTRY_NOTIFY_DIR override, and
// checks everything Task 9 promises at BOOT time (not the multi-minute
// fire/resolve journey itself, which TestFakeModeAlertDemoFiresThenResolves
// below proves against the engine directly, deterministically): every
// threshold rule seeds with a 60s/60s window, both demo webhook targets
// exist, and the notify channel already reads "ok" against its own
// fresh temp dir with zero configuration.
func TestRunFakeModeSeedsFastRulesAndDemoWebhookTargets(t *testing.T) {
	port := freePort(t)
	dbPath := filepath.Join(t.TempDir(), "g.db")
	env := map[string]string{
		"GANTRY_PORT":      fmt.Sprint(port),
		"GANTRY_DB_PATH":   dbPath,
		"GANTRY_FAKE_DATA": "1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }, "test-ver") }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("run did not shut down")
		}
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/api/healthz")
		if err != nil {
			return false
		}
		drainAndClose(resp)
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	rulesResp, err := http.Get(base + "/api/alerts/rules")
	require.NoError(t, err)
	defer drainAndClose(rulesResp)
	var rulesBody struct {
		Rules []server.AlertRuleDTO `json:"rules"`
	}
	require.NoError(t, json.NewDecoder(rulesResp.Body).Decode(&rulesBody))
	found := 0
	for _, r := range rulesBody.Rules {
		if r.Type != "threshold" {
			continue
		}
		found++
		require.EqualValues(t, 60, r.ForSeconds, "rule %q must seed fast in fake mode", r.ID)
		require.EqualValues(t, 60, r.ClearSeconds, "rule %q must seed fast in fake mode", r.ID)
	}
	require.Equal(t, 7, found, "all seven threshold builtins must have compressed windows")

	whResp, err := http.Get(base + "/api/alerts/webhooks")
	require.NoError(t, err)
	defer drainAndClose(whResp)
	var whBody struct {
		Targets []server.WebhookTargetDTO `json:"targets"`
	}
	require.NoError(t, json.NewDecoder(whResp.Body).Decode(&whBody))
	ids := map[string]bool{}
	for _, tgt := range whBody.Targets {
		ids[tgt.ID] = true
	}
	require.True(t, ids[fakeWebhookOKTargetID], "the always-succeeds demo target must be seeded")
	require.True(t, ids[fakeWebhookFailTargetID], "the always-fails demo target must be seeded")

	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/api/alerts")
		if err != nil {
			return false
		}
		defer drainAndClose(resp)
		var body struct {
			Channels map[string]string `json:"channels"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) != nil {
			return false
		}
		return body.Channels["notify"] == "ok"
	}, 3*time.Second, 50*time.Millisecond, "the notify channel must read ok against fake mode's own temp-dir default, no configuration needed")
}

// TestFakeModeAlertDemoFiresThenResolves wires the real fake.Generator
// straight into a real alert.Engine + the real buildDispatcher output --
// the actual production pieces, not a re-implementation -- and drives
// both through a synthetic clock advancing in the fake generator's own
// 2s cadence, so the whole ~6-minute demo schedule (disk4's temp ramp
// crossing disk-temp-high's fire threshold, then its cool-down crossing
// the clear threshold) proves out in a fast, deterministic test rather
// than a real wall-clock wait -- see internal/fake/fake.go's
// alertDemoDiskEntity doc for the exact schedule these tick counts walk.
func TestFakeModeAlertDemoFiresThenResolves(t *testing.T) {
	st := newAlertTestStore(t)
	require.NoError(t, st.SeedAlertRules(store.DefaultAlertRules(true)))

	fk := fake.New(st, st, 1)

	notifyDir := t.TempDir()
	getenv := func(k string) string {
		if k == "GANTRY_NOTIFY_DIR" {
			return notifyDir
		}
		return ""
	}
	cfg := config.New(st, getenv)
	dispatcher, err := buildDispatcher(st, cfg, getenv, "v-test", true)
	require.NoError(t, err)
	t.Cleanup(dispatcher.Stop)

	fleet := func() []alert.FleetMember {
		metas := fk.Metas()
		out := make([]alert.FleetMember, len(metas))
		for i, m := range metas {
			out[i] = alert.FleetMember{Name: m.Name, State: m.State, Health: m.Health}
		}
		return out
	}
	noClass := func(string, string) string { return "" }

	boot := time.Unix(1_700_000_000, 0)
	now := boot
	eng := alert.New(st, st.Live().MatchSince, noClass, fleet, dispatcher.Dispatch, func() time.Time { return now })

	tick := func(elapsedSeconds int) {
		now = boot.Add(time.Duration(elapsedSeconds) * time.Second)
		fk.Tick(now)
		require.NoError(t, eng.Tick(context.Background()))
	}

	// Ramp + hold: disk4 crosses disk-temp-high's 55°C fire threshold at
	// ~t=64s and stays above it, satisfying the fast-mode 60s sustained-
	// for window by ~t=124s (see alertDemoDiskEntity's own doc). 140s
	// gives comfortable margin.
	for s := 0; s <= 140; s += 2 {
		tick(s)
	}

	active, err := st.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	var firing *store.AlertInstance
	for i := range active {
		if active[i].RuleID == "disk-temp-high" && active[i].Entity == "disk4" {
			firing = &active[i]
		}
	}
	require.NotNil(t, firing, "disk-temp-high must be firing on disk4 by t=140s")
	require.Equal(t, "firing", firing.State)
	require.Greater(t, firing.Value, 55.0)

	// The frame's own alerts block (main.go's buildAlertsBlock) must
	// also see it -- the same data the Overview headline/Alerts view
	// read live, capped and joined with the rule name.
	block := buildAlertsBlock(st, dispatcher)
	sawDisk4 := false
	for _, f := range block.Firing {
		if f.RuleID == "disk-temp-high" && f.Entity == "disk4" {
			sawDisk4 = true
			require.Equal(t, "Disk temperature high", f.RuleName)
		}
	}
	require.True(t, sawDisk4, "the frame's alerts block must carry the firing disk4 instance")

	// At least one delivery (the "fired" notification, through the real
	// notify-spool channel) must have landed by now.
	require.Eventually(t, func() bool {
		deliveries, derr := st.LastDeliveries(context.Background(), 20)
		if derr != nil {
			return false
		}
		for _, d := range deliveries {
			if d.Channel == "notify" && d.OK {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "the fired notification must reach the notify spool")

	// Cool-down: disk4 crosses back below the 50°C clear threshold at
	// ~t=310s and stays below it, satisfying the 60s clear window by
	// ~t=370s. 420s gives comfortable margin.
	for s := 142; s <= 420; s += 2 {
		tick(s)
	}

	history, err := st.AlertHistory(context.Background(), 0, 0, 50)
	require.NoError(t, err)
	var resolved *store.AlertInstance
	for i := range history {
		if history[i].RuleID == "disk-temp-high" && history[i].Entity == "disk4" {
			resolved = &history[i]
		}
	}
	require.NotNil(t, resolved, "disk-temp-high on disk4 must have resolved by t=420s")
	require.Equal(t, "resolved", resolved.State)
	require.Equal(t, "cleared", resolved.ResolveReason)
	require.Less(t, resolved.Value, 50.0)

	activeAfter, err := st.ActiveAlertInstances(context.Background())
	require.NoError(t, err)
	for _, inst := range activeAfter {
		require.False(t, inst.RuleID == "disk-temp-high" && inst.Entity == "disk4", "the resolved instance must no longer be active")
	}

	require.Eventually(t, func() bool {
		deliveries, derr := st.LastDeliveries(context.Background(), 20)
		if derr != nil {
			return false
		}
		for _, d := range deliveries {
			if d.Channel == "notify" && d.Phase == "resolved" && d.OK {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "the resolved notification must also reach the notify spool")
}
