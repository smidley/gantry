package gpu

import (
	"bytes"
	"context"
	"fmt"
	"log"
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

// fdLinkPath mirrors fdinfoPath for the other half of a real /proc/<pid>
// entry: the fd/<n> symlink whose target scanClients' readlink prefilter
// inspects before deciding whether fdinfo/<n> is worth opening at all.
func fdLinkPath(procRoot, pid, fd string) string {
	return filepath.Join(procRoot, pid, "fd", fd)
}

// writeFDLink creates the fd/<n> symlink half of a fake /proc/<pid> entry,
// pointing at target (e.g. "/dev/dri/renderD128" or "socket:[123]"); the
// target need not exist, matching real /proc symlinks that dangle once
// the referenced file is gone.
func writeFDLink(t *testing.T, procRoot, pid, fd, target string) {
	t.Helper()
	path := fdLinkPath(procRoot, pid, fd)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.Symlink(target, path))
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

// TestGPUMetaResolvesVendorAndDriverPerEntity pins the card-title fix
// (item 4): a full scan (Tick's own first pass) must resolve each newly
// -seen pdev's vendor from SysRoot and carry the already-known driver
// (i915FDInfo's own "i915") up from the client to the entity level.
func TestGPUMetaResolvesVendorAndDriverPerEntity(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))

	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:00:02.0", "0x8086\n")

	c := New(newFakeSink(), procRoot, func(string) (string, bool) { return "", false })
	c.SysRoot = sysRoot
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	require.Equal(t, map[string]EntityMeta{"0000:00:02.0": {Vendor: "Intel", Driver: "i915"}}, c.GPUMeta())
}

// TestGPUMetaFallsBackToGpu0EntityWhenClientHasNoPdev mirrors
// tickClients' own "gpu0" fallback (used when a client's fdinfo carries
// no drm-pdev field at all) -- GPUMeta must key by that same fallback id,
// and vendorNameForPdev's own missing-file fallback ("GPU") applies
// since "gpu0" is never a real sysfs path.
func TestGPUMetaFallsBackToGpu0EntityWhenClientHasNoPdev(t *testing.T) {
	procRoot := t.TempDir()
	// Deliberately no "drm-pdev:" line -- ParseFDInfo only requires
	// drm-driver/drm-client-id.
	writeFile(t, fdinfoPath(procRoot, "100", "0"), "drm-driver:\tamdgpu\ndrm-client-id:\t10\ndrm-engine-gfx:\t1000000000 ns\n")

	c := New(newFakeSink(), procRoot, func(string) (string, bool) { return "", false })
	c.SysRoot = t.TempDir() // empty -- no vendor file for "gpu0" either way
	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	require.Equal(t, map[string]EntityMeta{"gpu0": {Vendor: "GPU", Driver: "amdgpu"}}, c.GPUMeta())
}

// TestGPUMetaRemembersEntityAcrossFullScans pins fullScan/noteEntityMeta's
// own "resolved once, never re-resolved" contract: a second full scan
// (simulated here by calling Tick again past fullScanInterval) must not
// forget an entity whose only client has since gone idle/exited -- real
// hardware doesn't change identity just because nothing is using it
// this instant.
func TestGPUMetaRemembersEntityAcrossFullScans(t *testing.T) {
	procRoot := t.TempDir()
	fdPath := fdinfoPath(procRoot, "100", "0")
	writeFile(t, fdPath, i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	sysRoot := t.TempDir()
	writePCIVendorFile(t, sysRoot, "0000:00:02.0", "0x8086\n")

	c := New(newFakeSink(), procRoot, func(string) (string, bool) { return "", false })
	c.SysRoot = sysRoot
	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))
	require.NoError(t, os.Remove(fdPath)) // the client goes away entirely

	// Force a second full scan (fullScanInterval is 30s).
	t1 := t0.Add(31 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))
	require.Empty(t, c.clients, "the client itself is gone")
	require.Equal(t, map[string]EntityMeta{"0000:00:02.0": {Vendor: "Intel", Driver: "i915"}}, c.GPUMeta(), "the entity's own meta must survive its last client disappearing")
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

// TestScanClientsOnlyOpensDRMFdinfoCandidates pins Task 3's perf fix: a
// full scan must readlink /proc/<pid>/fd/<n> before opening
// fdinfo/<n>, and skip any fd whose target doesn't contain "/dev/dri/".
// fd 1's fdinfo is deliberately a byte-identical, otherwise-valid DRM
// client (client-id "11") -- the ONLY thing distinguishing it from fd 0
// is its fd/ symlink target, so this fails if the prefilter isn't
// actually gating the open (as opposed to, say, some content-based
// filter that would happen to also reject it).
func TestScanClientsOnlyOpensDRMFdinfoCandidates(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFDLink(t, procRoot, "100", "0", "/dev/dri/renderD128")
	writeFile(t, fdinfoPath(procRoot, "100", "1"), i915FDInfo("11", "0000:00:02.0", 1_000_000_000))
	writeFDLink(t, procRoot, "100", "1", "socket:[123]")

	clients := scanClients(procRoot)
	require.Len(t, clients, 1)
	require.Contains(t, clients, "10")
	require.NotContains(t, clients, "11", "the non-DRM fd's fdinfo must never even be opened")
}

// TestScanClientsFallsBackToOpenEverythingWhenReadlinkFailsForWholePID
// pins the other half of Task 3's contract: when readlink can't be used
// at all for a pid (here, simulated by there being no fd/ directory —
// the same shape a permission-denied ENOENT/EACCES on every entry would
// produce), the prefilter must not suppress discovery; every fdinfo/<n>
// gets opened, same as before Task 3.
func TestScanClientsFallsBackToOpenEverythingWhenReadlinkFailsForWholePID(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	// deliberately no fd/ directory at all under procRoot/100.

	clients := scanClients(procRoot)
	require.Len(t, clients, 1)
	require.Contains(t, clients, "10")
}

// TestScanClientsPartialReadlinkFailureStillFiltersTheRest confirms the
// fallback is scoped to "readlink unusable for this whole pid", not
// triggered by one fd among several failing (e.g. a fd that raced closed
// between the fdinfo and fd listings) -- the other fds' real prefilter
// result must still be honored.
func TestScanClientsPartialReadlinkFailureStillFiltersTheRest(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 1_000_000_000))
	writeFDLink(t, procRoot, "100", "0", "/dev/dri/renderD128")
	writeFile(t, fdinfoPath(procRoot, "100", "1"), i915FDInfo("11", "0000:00:02.0", 1_000_000_000))
	writeFDLink(t, procRoot, "100", "1", "socket:[123]")
	// fd 2 has fdinfo but no fd/2 symlink at all -- an individual
	// readlink failure alongside two that succeeded.
	writeFile(t, fdinfoPath(procRoot, "100", "2"), i915FDInfo("12", "0000:00:02.0", 1_000_000_000))

	clients := scanClients(procRoot)
	require.Len(t, clients, 1)
	require.Contains(t, clients, "10")
	require.NotContains(t, clients, "11")
	require.NotContains(t, clients, "12", "an individual readlink failure must not fall back to opening that fd when siblings resolved fine")
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

// TestEngineBusyPctClampedAtEmission pins the [0,100] clamp: a counter
// delta larger than one tick interval's worth of ns -- a driver overshoot
// (~100.001% seen live, per the carry-in doc) synthesized here as a much
// larger 150% delta so the un-clamped value would be unmistakable -- must
// never reach the sink above 100, for both the per-container series and
// the per-GPU total (the sum of every client sharing that GPU).
func TestEngineBusyPctClampedAtEmission(t *testing.T) {
	procRoot := t.TempDir()
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 0))
	writeFile(t, cgroupPath(procRoot, "100"), "0::/docker/"+jellyfinID+"\n")

	sink := newFakeSink()
	c := New(sink, procRoot, dockerLookup(jellyfinID, "jellyfin"))

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))

	// Over the next 2s, the counter advances by 3s worth of ns: a
	// physically-impossible-but-observed-live overshoot that would
	// naively compute to 150% ((3e9 ns / 2s) / 1e7).
	writeFile(t, fdinfoPath(procRoot, "100", "0"), i915FDInfo("10", "0000:00:02.0", 3_000_000_000))
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))

	jellyfinPct, ok := sink.value("container", "jellyfin", "gpu.video.busy_pct")
	require.True(t, ok)
	require.Equal(t, 100.0, jellyfinPct, "per-container busy_pct must clamp at 100, not report the naive 150%")

	gpuPct, ok := sink.value("gpu", "0000:00:02.0", "engine.video.busy_pct")
	require.True(t, ok)
	require.Equal(t, 100.0, gpuPct, "per-gpu total busy_pct must clamp at 100 too")
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

// TestEngineCapacityKeysSkippedSilently pins Task 3's fix: xe reports
// drm-engine-capacity-<name> fields (engine instance counts, not
// busy-time counters) alongside real drm-engine-<name> fields for the
// same engine. capacity-* must never reach engineBusyPct or emit any
// metric — it must not even count as "seen" toward the once-only
// non-ns warning, which the next test pins directly.
func TestEngineCapacityKeysSkippedSilently(t *testing.T) {
	procRoot := t.TempDir()
	content := "drm-driver:\txe\ndrm-client-id:\t50\ndrm-pdev:\t0000:00:03.0\ndrm-engine-capacity-render:\t1\ndrm-engine-render:\t1000000000 ns\n"
	writeFile(t, fdinfoPath(procRoot, "500", "0"), content)
	writeFile(t, cgroupPath(procRoot, "500"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	t0 := time.Unix(1000, 0)
	require.NoError(t, c.Tick(context.Background(), t0))
	content = "drm-driver:\txe\ndrm-client-id:\t50\ndrm-pdev:\t0000:00:03.0\ndrm-engine-capacity-render:\t1\ndrm-engine-render:\t1200000000 ns\n"
	writeFile(t, fdinfoPath(procRoot, "500", "0"), content)
	t1 := t0.Add(2 * time.Second)
	require.NoError(t, c.Tick(context.Background(), t1))

	_, hasCapacity := sink.value("gpu", "0000:00:03.0", "engine.capacity-render.busy_pct")
	require.False(t, hasCapacity, "capacity-* is not a busy-time counter and must never be recorded")
	pct, ok := sink.value("gpu", "0000:00:03.0", "engine.render.busy_pct")
	require.True(t, ok, "the real render counter alongside it must still be recorded")
	require.InDelta(t, 10.0, pct, 1e-9)
}

// TestEngineCapacityKeyDoesNotBurnNonNanosecondWarningBudget goes one
// step further than the previous test: it proves capacity-render is
// skipped BEFORE engineBusyPct's non-ns check, not merely that it
// happens not to emit a metric. drm-engine-capacity-render has no unit
// suffix at all ("1", not "1 ns"), so if it ever reached engineBusyPct
// it would consume the collector's one-shot warnNonNS log -- leaving a
// genuinely novel non-ns engine ("weird") silent for the rest of the
// process's life. Go's map iteration order is randomized, so without
// the fix this test would fail nondeterministically (whenever
// capacity-render happened to be visited before weird); the fix makes
// the outcome independent of iteration order.
func TestEngineCapacityKeyDoesNotBurnNonNanosecondWarningBudget(t *testing.T) {
	procRoot := t.TempDir()
	content := "drm-driver:\txe\ndrm-client-id:\t60\ndrm-pdev:\t0000:00:04.0\ndrm-engine-capacity-render:\t1\ndrm-engine-weird:\t999 cycles\n"
	writeFile(t, fdinfoPath(procRoot, "600", "0"), content)
	writeFile(t, cgroupPath(procRoot, "600"), "0::/init.scope\n")

	sink := newFakeSink()
	c := New(sink, procRoot, func(string) (string, bool) { return "", false })

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	require.NoError(t, c.Tick(context.Background(), time.Unix(1000, 0)))

	require.Contains(t, logBuf.String(), `engine "weird"`, "the once-log must still fire for the genuinely novel non-ns engine")
	require.NotContains(t, logBuf.String(), "capacity-render", "capacity-* must never reach the non-ns check at all")
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
