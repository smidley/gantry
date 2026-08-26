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
	defer l.Close()
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
	resp.Body.Close()
}

// TestBuildSnapshotGroupsSamplesByKindAndSkipsLivePrefixed exercises the
// real buildSnapshot closure used in main wiring — offline, with no
// daemon or /proc required: dc.Running()/ur.Version() on freshly
// constructed (never-ticked) collectors deterministically return their
// zero state, so this only needs a real *store.Store fed by hand.
func TestBuildSnapshotGroupsSamplesByKindAndSkipsLivePrefixed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	st.Record(store.SeriesKey{Kind: "host", Metric: "cpu.total"}, 1000, 12.5)
	st.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "cpu.pct"}, 1000, 4.2)
	st.Record(store.SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "live:io.sda.read_bps"}, 1000, 55555)
	st.Record(store.SeriesKey{Kind: "disk", Entity: "disk1", Metric: "temp.c"}, 1000, 31)
	st.Record(store.SeriesKey{Kind: "gpu", Entity: "gpu0", Metric: "engine.render.busy_pct"}, 1000, 5.5)
	st.Record(store.SeriesKey{Kind: "unraid", Entity: "docker", Metric: "docker.images_bytes"}, 1000, 999)

	dc := docker.New(st, st, st.Live().Evict, "/var/run/docker.sock")
	ur := unraid.New(st, st, t.TempDir(), "/proc")

	snap := buildSnapshot(st, dc, ur)()

	require.Equal(t, 12.5, snap.Host["cpu.total"])
	require.Equal(t, 4.2, snap.Containers["jellyfin"].Metrics["cpu.pct"])
	require.Equal(t, 31.0, snap.Disks["disk1"]["temp.c"])
	require.Equal(t, 5.5, snap.GPU["gpu0"]["engine.render.busy_pct"])
	require.Equal(t, 999.0, snap.Unraid["docker.images_bytes"])
	require.Equal(t, "", snap.UnraidVersion, "unraid.Version() before any Tick is empty")
	require.Empty(t, dc.Running(), "docker.Running() before any Tick is empty")

	for _, c := range snap.Containers {
		for metric := range c.Metrics {
			require.False(t, strings.HasPrefix(metric, "live:"), "live:-prefixed metrics must never reach the snapshot")
		}
	}
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
	defer l.Close() // hold the port for the whole test so run()'s bind fails
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
