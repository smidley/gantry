package gpu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeSink captures every recorded sample, keyed by its full SeriesKey
// (last write wins per key). Unlike the single-Kind fakeSinks in
// host/docker, this collector writes both "container" and "gpu" kind
// series, so value() takes Kind explicitly.
type fakeSink struct {
	records map[store.SeriesKey]float64
}

func newFakeSink() *fakeSink { return &fakeSink{records: make(map[store.SeriesKey]float64)} }

func (f *fakeSink) Record(key store.SeriesKey, ts int64, val float64) {
	f.records[key] = val
}

func (f *fakeSink) value(kind, entity, metric string) (float64, bool) {
	v, ok := f.records[store.SeriesKey{Kind: kind, Entity: entity, Metric: metric}]
	return v, ok
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func fdinfoPath(procRoot, pid, fd string) string {
	return filepath.Join(procRoot, pid, "fdinfo", fd)
}

func cgroupPath(procRoot, pid string) string {
	return filepath.Join(procRoot, pid, "cgroup")
}

// i915FDInfo builds one fdinfo file's content: a DRM client with a single
// drm-engine-video counter (nanoseconds), matching the real i915 shape
// captured in fdinfo_test.go's fixtures.
func i915FDInfo(clientID, pdev string, videoNs uint64) string {
	return fmt.Sprintf("drm-driver:\ti915\ndrm-client-id:\t%s\ndrm-pdev:\t%s\ndrm-engine-video:\t%d ns\n", clientID, pdev, videoNs)
}

// dockerLookup returns an injected lookup mapping exactly one container id
// to one name; anything else misses (host bucket).
func dockerLookup(id, name string) func(string) (string, bool) {
	return func(candidate string) (string, bool) {
		if candidate == id {
			return name, true
		}
		return "", false
	}
}

const jellyfinID = "aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999"

func TestGPUNameAndInterval(t *testing.T) {
	c := New(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	require.Equal(t, "gpu", c.Name())
	require.Equal(t, 2*time.Second, c.Interval())
}

func TestGPUProbeAvailableWhenProcRootWalkable(t *testing.T) {
	c := New(newFakeSink(), t.TempDir(), func(string) (string, bool) { return "", false })
	require.True(t, c.Probe(context.Background()).Available)
}

func TestGPUProbeUnavailableWhenProcRootMissing(t *testing.T) {
	c := New(newFakeSink(), filepath.Join(t.TempDir(), "does-not-exist"), func(string) (string, bool) { return "", false })
	status := c.Probe(context.Background())
	require.False(t, status.Available)
	require.NotEmpty(t, status.Detail)
}

// Dedupe: the same drm-client-id reachable via two different fds (two fds
// of the same open DRM file, or two pids sharing it) must count once.
func TestScanClientsDedupesSameClientIDAcrossTwoFDs(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFile(t, fdinfoPath(procRoot, "100", "3"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))

	clients := scanClients(procRoot)
	require.Len(t, clients, 1)
	require.Contains(t, clients, "10")
	require.Equal(t, "i915", clients["10"].Driver)
	require.Equal(t, "0000:00:02.0", clients["10"].Pdev)
}

// The core mechanics test: one containerized client and one real
// host-side client (spike S1's finding) sharing a GPU. Two ticks 2s
// apart, hand-advanced counters, exact busy_pct assertions.
func TestTickTwiceComputesPerContainerAndGPUTotalsWithHostBucketing(t *testing.T) {
	procRoot := t.TempDir()

	// pid 100: containerized client (jellyfin), client-id 10.
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFile(t, cgroupPath(procRoot, "100"), "0::/docker/"+jellyfinID+"\n")

	// pid 200: real host-side DRM client, client-id 20, same GPU.
	writeFile(t, fdinfoPath(procRoot, "200", "0"), i915FDInfo("20", "0000:00:02.0", 500_000_000))
	writeFile(t, cgroupPath(procRoot, "200"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, dockerLookup(jellyfinID, "jellyfin"))

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))

	_, ok := sink.value("container", "jellyfin", "gpu.video.busy_pct")
	require.False(t, ok, "first tick only seeds the rate tracker, it must not emit")
	_, ok = sink.value("gpu", "0000:00:02.0", "engine.video.busy_pct")
	require.False(t, ok, "first tick only seeds the rate tracker, it must not emit")

	// Advance both counters over a 2s window: jellyfin +200,000,000ns (10%),
	// host client +400,000,000ns (20%).
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_200_000_000))
	writeFile(t, fdinfoPath(procRoot, "200", "0"), i915FDInfo("20", "0000:00:02.0", 900_000_000))

	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))

	jellyfinPct, ok := sink.value("container", "jellyfin", "gpu.video.busy_pct")
	require.True(t, ok)
	require.InDelta(t, 10.0, jellyfinPct, 1e-9, "jellyfin's own client delta only")

	gpuPct, ok := sink.value("gpu", "0000:00:02.0", "engine.video.busy_pct")
	require.True(t, ok)
	require.InDelta(t, 30.0, gpuPct, 1e-9, "gpu totals must include both the container client (10%) and the host-bucket client (20%)")

	for key := range sink.records {
		if key.Kind == "container" {
			require.Equal(t, "jellyfin", key.Entity, "the host-bucket client must never surface as a container series")
		}
	}
}

// A client whose fdinfo file disappears (process exited, fd closed) must
// be dropped immediately on the tick that discovers this, not left
// dangling until the next 30s full scan, and must not error the tick.
func TestDeadClientDroppedOnNextTickWithoutError(t *testing.T) {
	procRoot := t.TempDir()
	fdPath := fdinfoPath(procRoot, "100", "0")
	writeFile(t, fdPath, i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFile(t, cgroupPath(procRoot, "100"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))
	require.Len(t, c.clients, 1)

	require.NoError(t, os.Remove(fdPath))

	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))
	require.Empty(t, c.clients, "an unreadable client must be dropped immediately")
}

// TestDeadClientDropEvictsItsRateTrackerKeys pins Task 2: a client
// dropped via the dead-read path (fdinfo unreadable) must also have its
// RateTracker entries (clientID+"."-prefixed, one per engine) evicted
// right away — otherwise the tracker map grows by one entry per engine
// per client for the life of the process as clients come and go.
func TestDeadClientDropEvictsItsRateTrackerKeys(t *testing.T) {
	procRoot := t.TempDir()
	fdPath := fdinfoPath(procRoot, "100", "0")
	writeFile(t, fdPath, i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFile(t, cgroupPath(procRoot, "100"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))
	writeFile(t, fdPath, i915FDInfo("10", "0000:00:02.0", 1_200_000_000))
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))
	require.Greater(t, c.rates.Len(), 0, "the client's engine counter must have seeded a rate-tracker key")

	require.NoError(t, os.Remove(fdPath))
	t2 := t1.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t2))

	require.Equal(t, 0, c.rates.Len(), "the dead client's rate-tracker keys must be evicted with it")
}

// TestFullScanEvictsRateTrackerKeysForClientsThatVanished pins Task 2's
// other eviction path: a client that simply stops appearing in a 30s
// full scan (wholesale c.clients replacement) — as opposed to failing a
// 2s fdinfo re-read — must also have its rate-tracker keys evicted, by
// diffing the outgoing client-id set against the incoming one.
func TestFullScanEvictsRateTrackerKeysForClientsThatVanished(t *testing.T) {
	procRoot := t.TempDir()
	fdPath := fdinfoPath(procRoot, "100", "0")
	writeFile(t, fdPath, i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFile(t, cgroupPath(procRoot, "100"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0)) // first full scan discovers client "10"
	writeFile(t, fdPath, i915FDInfo("10", "0000:00:02.0", 1_200_000_000))
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))
	require.Greater(t, c.rates.Len(), 0)

	// Client "10" vanishes (process gone, fd closed) without the 2s
	// dead-read path ever noticing first — the very next tick is a fresh
	// full scan (30s elapsed) that simply never finds it again.
	require.NoError(t, os.RemoveAll(filepath.Join(procRoot, "100")))
	t2 := t1.Add(30 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t2))

	require.Empty(t, c.clients)
	require.Equal(t, 0, c.rates.Len(), "a client dropped by full-scan replacement must have its rate keys evicted too")
}

// TestChurnManyGPUClientsRateTrackerReturnsToBaseline simulates N DRM
// clients appearing and disappearing across full scans and asserts the
// RateTracker's key count returns to its pre-churn baseline every time,
// not just for one client in isolation.
func TestChurnManyGPUClientsRateTrackerReturnsToBaseline(t *testing.T) {
	procRoot := t.TempDir()
	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0)) // baseline: no clients at all
	baseline := c.rates.Len()

	const n = 50
	tick := t0
	for i := 0; i < n; i++ {
		pid := fmt.Sprintf("%d", 1000+i)
		clientID := fmt.Sprintf("client%d", i)
		fdPath := fdinfoPath(procRoot, pid, "0")
		writeFile(t, fdPath, i915FDInfo(clientID, "0000:00:02.0", 1_000_000_000))
		writeFile(t, cgroupPath(procRoot, pid), "0::/init.scope\n")

		tick = tick.Add(30 * time.Second) // force a full rescan every iteration
		require.NoError(t, c.Tick(context.Background(), tick))
		tick = tick.Add(2 * time.Second)
		writeFile(t, fdPath, i915FDInfo(clientID, "0000:00:02.0", 1_200_000_000))
		require.NoError(t, c.Tick(context.Background(), tick))

		require.NoError(t, os.RemoveAll(filepath.Join(procRoot, pid)))
		tick = tick.Add(30 * time.Second) // force the full scan that no longer finds it
		require.NoError(t, c.Tick(context.Background(), tick))
	}

	require.Equal(t, baseline, c.rates.Len(), "churned GPU clients must not accumulate rate-tracker keys")
}

// TestEngineNameHyphenPreservedThroughSlugging pins Task 1's hygiene fix
// for engine names: "-" is in SlugSegment's allowed charset, so a real
// engine name like i915's "video-enhance" must reach the metric name
// unchanged.
func TestEngineNameHyphenPreservedThroughSlugging(t *testing.T) {
	procRoot := t.TempDir()
	content := "drm-driver:\ti915\ndrm-client-id:\t40\ndrm-pdev:\t0000:00:02.0\ndrm-engine-video-enhance:\t1000000000 ns\n"
	writeFile(t, fdinfoPath(procRoot, "400", "0"), content)
	writeFile(t, cgroupPath(procRoot, "400"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))

	content = "drm-driver:\ti915\ndrm-client-id:\t40\ndrm-pdev:\t0000:00:02.0\ndrm-engine-video-enhance:\t1200000000 ns\n"
	writeFile(t, fdinfoPath(procRoot, "400", "0"), content)
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))

	pct, ok := sink.value("gpu", "0000:00:02.0", "engine.video-enhance.busy_pct")
	require.True(t, ok, "engine name must keep its hyphen after slugging")
	require.InDelta(t, 10.0, pct, 1e-9)
}

// TestEngineNameIsSlugged pins the other half of Task 1's hygiene fix:
// an engine name isn't guaranteed clean (it comes straight off a
// kernel/driver-supplied fdinfo key), so it must go through SlugSegment
// like every other dynamic metric-name segment. No real driver is known
// to report a mixed-case engine today; this exercises the safety net
// directly against a synthetic dirty name.
func TestEngineNameIsSlugged(t *testing.T) {
	procRoot := t.TempDir()
	content := "drm-driver:\ti915\ndrm-client-id:\t41\ndrm-pdev:\t0000:00:02.0\ndrm-engine-RENDER:\t1000000000 ns\n"
	writeFile(t, fdinfoPath(procRoot, "410", "0"), content)
	writeFile(t, cgroupPath(procRoot, "410"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))

	content = "drm-driver:\ti915\ndrm-client-id:\t41\ndrm-pdev:\t0000:00:02.0\ndrm-engine-RENDER:\t1200000000 ns\n"
	writeFile(t, fdinfoPath(procRoot, "410", "0"), content)
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))

	pct, ok := sink.value("gpu", "0000:00:02.0", "engine.render.busy_pct")
	require.True(t, ok, "engine name must be slugged (lowercased) before entering the metric name")
	require.InDelta(t, 10.0, pct, 1e-9)
}

// Engine counters not reported in nanoseconds (the xe driver's cycle
// counters) aren't yet supported and must be skipped rather than
// misinterpreted, and must not emit anything.
func TestNonNanosecondEngineUnitsSkipped(t *testing.T) {
	procRoot := t.TempDir()
	content := "drm-driver:\txe\ndrm-client-id:\t30\ndrm-pdev:\t0000:00:02.0\ndrm-engine-render:\t12345 cycles\n"
	writeFile(t, fdinfoPath(procRoot, "300", "0"), content)
	writeFile(t, cgroupPath(procRoot, "300"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))

	require.Empty(t, sink.records, "non-nanosecond engine counters must be skipped, not emitted")
}
