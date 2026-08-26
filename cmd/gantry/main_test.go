package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/collect/unraid"
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
	// use, so the snapshot may or may not be empty depending on timing —
	// only the response shape is asserted here.
	snapResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/live/snapshot", port))
	require.NoError(t, err)
	var snapBody struct {
		TS int64 `json:"ts"`
	}
	require.NoError(t, json.NewDecoder(snapResp.Body).Decode(&snapBody))
	drainAndClose(snapResp)
	require.Equal(t, http.StatusOK, snapResp.StatusCode)
	require.Greater(t, snapBody.TS, int64(0))

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
	sources := func() map[string]string { return map[string]string{"host": "ok"} }

	snap := buildSnapshot(st, dc, ur, sources)()

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

	// jellyfin is neither running (dc never ticked) nor known by name
	// (dc's registry has never seen it) -- its ancient (year-1970) sample
	// must not resurrect it into the frame. This is the buildSnapshot-level
	// half of the stopped-container filter pin; containerFrameEntities'
	// own tests (below) exercise the freshness/lookup rule directly.
	_, stillPresent := snap.Containers["jellyfin"]
	require.False(t, stillPresent, "a container dc doesn't know about must not appear in the frame")

	for _, c := range snap.Containers {
		for metric := range c.Metrics {
			require.False(t, strings.HasPrefix(metric, "live:"), "live:-prefixed metrics must never reach the snapshot")
		}
	}
}

// fakeMeta builds a minimal known-container answer for a lookupByName
// stand-in, without needing a real docker.Collector/daemon.
func fakeMeta(name string) docker.Meta { return docker.Meta{Name: name} }

// TestContainerFrameEntitiesIncludesRunningRegardlessOfLookup pins the OR's
// first clause: a name in `running` is included unconditionally — the
// lookup function must not even be consulted for it (a call for this name
// fails the test immediately, proving the OR short-circuits).
func TestContainerFrameEntitiesIncludesRunningRegardlessOfLookup(t *testing.T) {
	running := map[string]struct{}{"jellyfin": {}}
	lookup := func(name string) (docker.Meta, bool) {
		t.Fatalf("lookupByName must not be consulted for a running container, got %q", name)
		return docker.Meta{}, false
	}

	got := containerFrameEntities(running, map[string]int64{}, 60, lookup)
	require.Contains(t, got, "jellyfin")
}

// TestContainerFrameEntitiesIncludesFreshKnownNonRunning pins the OR's
// second clause: a non-running name with a live sample younger than
// maxAge AND a known lookup result is included.
func TestContainerFrameEntitiesIncludesFreshKnownNonRunning(t *testing.T) {
	lookup := func(name string) (docker.Meta, bool) { return fakeMeta(name), name == "radarr" }

	got := containerFrameEntities(map[string]struct{}{}, map[string]int64{"radarr": 59}, 60, lookup)
	require.Contains(t, got, "radarr")
}

// TestContainerFrameEntitiesExcludesStaleEvenWhenKnown pins the
// "stopped-and-gone" cutoff itself: once a non-running container's
// freshest sample is 60s old or older, it drops out of the frame even
// though lookupByName still recognizes the name (registry cleanup and
// the frame's own 60s cutoff are two different clocks).
func TestContainerFrameEntitiesExcludesStaleEvenWhenKnown(t *testing.T) {
	lookup := func(name string) (docker.Meta, bool) { return fakeMeta(name), true }

	got := containerFrameEntities(map[string]struct{}{}, map[string]int64{"radarr": 60}, 60, lookup)
	require.NotContains(t, got, "radarr", "age >= maxAge must exclude, not just age > maxAge")
}

// TestContainerFrameEntitiesExcludesFreshButUnknown pins the other half:
// a stopped-AND-REMOVED container's lingering fresh sample must not
// resurrect it once dc no longer knows the name at all.
func TestContainerFrameEntitiesExcludesFreshButUnknown(t *testing.T) {
	lookup := func(string) (docker.Meta, bool) { return docker.Meta{}, false }

	got := containerFrameEntities(map[string]struct{}{}, map[string]int64{"radarr": 0}, 60, lookup)
	require.NotContains(t, got, "radarr", "a name the registry no longer knows must drop immediately")
}

// TestContainerFrameEntitiesRunningWinsOverStaleSample confirms a name
// present in both `running` and `sampleAge` (the common case: a running
// container that also has metric samples) is included via the running
// clause and isn't accidentally excluded by a stale sampleAge entry.
func TestContainerFrameEntitiesRunningWinsOverStaleSample(t *testing.T) {
	running := map[string]struct{}{"jellyfin": {}}
	lookup := func(string) (docker.Meta, bool) { return docker.Meta{}, false }

	got := containerFrameEntities(running, map[string]int64{"jellyfin": 99999}, 60, lookup)
	require.Contains(t, got, "jellyfin")
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
