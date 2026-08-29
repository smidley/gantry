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

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/smidley/gantry/internal/collect/gpu"
	"github.com/smidley/gantry/internal/collect/host"
	"github.com/smidley/gantry/internal/collect/unraid"
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
	gp := gpu.New(st, "/proc", func(string) (string, bool) { return "", false })
	nv := gpu.NewNvidia(st, "/proc", func(string) (string, bool) { return "", false })
	sources := func() map[string]string { return map[string]string{"host": "ok"} }

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, nil)() // nil fakeMetas/fakeDiskMeta: not exercising the fake-mode path here

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

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil)()

	require.Empty(t, dc.Running(), "dc's own registry never saw this container -- the fix must not depend on it")
	c, ok := snap.Containers["jellyfin"]
	require.True(t, ok, "a fake Meta must survive the DTO-v2 filter unconditionally, not as a fallback lookup")
	require.Equal(t, "running", c.State)
	require.Equal(t, "healthy", c.Health)
	require.Equal(t, "demo/jellyfin:latest", c.Image)
	require.Equal(t, 4.2, c.Metrics["cpu.pct"])
}

// TestBuildSnapshotDropsStaleSampleFromRunningContainer pins the
// per-sample freshness gate a running container's metrics still need:
// containerFrameEntities/include only decide whether the ENTITY belongs
// in the frame, so a still-running container's own individual samples
// were previously included unconditionally, no matter how old. That let
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

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil)()

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

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, fakeMetas, nil)()

	c, ok := snap.Containers["gitea"]
	require.True(t, ok, "a stopped-but-known container must still appear in the frame")
	require.Equal(t, "exited", c.State)
	require.Equal(t, "demo/gitea:latest", c.Image)
	require.Empty(t, c.Metrics, "no live samples were recorded for it -- Metrics must not be fabricated")
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

	snap := buildSnapshot(st, dc, ur, gp, nv, sources, nil, fakeDiskMeta)()

	require.Equal(t, server.DiskMetaDTO{Device: "sdc", Kind: "hdd"}, snap.DiskMeta["disk1"], "the real unraid collector's own DiskMeta must survive into the DTO")
	require.Equal(t, server.DiskMetaDTO{Device: "sdi", Kind: "usb"}, snap.DiskMeta["flash"], "fake mode's own DiskMeta overlay must land alongside it, not replace it")
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

// TestContainerStorageResolvesMountsAndDeviceIO pins containerStorage's
// full happy path: a hand-built lookupMeta/poolSlots pair (no real
// docker.Collector registry or daemon needed, the same reason
// containerFrameEntities takes lookupByName as a parameter) plus a bare
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
