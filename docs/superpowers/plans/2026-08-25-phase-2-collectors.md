# Gantry Phase 2: Collectors — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Real data end to end — docker, host, Unraid, and GPU collectors feeding the Phase 1 store on a live Unraid box, with a snapshot API to see it, collector isolation per spec §9, and the Phase 1 tech-debt carry-ins retired.

**Architecture:** A small collector framework (probe / tick / backoff / panic isolation) runs each source on its own ticker and reports availability into healthz's `sources` map. Collectors write through the existing `store.MetricSink` and `AppendEvent` — the store does not change shape. The docker collector owns the container registry (id → name/pid/state) that the network and GPU collectors consume. GPU uses the fdinfo mechanics proven by spike S1 (i915 ns counters; unmapped clients bucketed as host). PSI is feature-detected (two tiers per spikes.md).

**Tech Stack:** Go ≥1.25 (floor unchanged), `github.com/docker/docker` client (the ONE new dependency), stdlib everywhere else. No cgo, ever.

**Spec:** `docs/superpowers/specs/2026-08-25-gantry-design.md` (+ spikes: `docs/superpowers/spikes.md`)

## Phase context

Phase 1 (merged, `main`@94f5962) shipped the store (ring → 1m/10m/1h tiers), config, fake mode, server skeleton, scratch image, and the parsers (`gpu.ParseFDInfo`, `cgroup.ContainerID`, `alert.WriteNotify`). Phase 3 (SSE + full UI — **including spec §4.1's container log streaming**, which is an API/UI surface, not a stored metric) and Phase 4 (alert engine + CA release) follow. Phase 5 is the insights engine; **this phase collects its enablers** (PSI where available, per-device IO, throttle counters).

## Global Constraints

- Series identity: `SeriesKey{Kind, Entity, Metric}`; Kind ∈ host|container|disk|gpu|unraid; container Entity = container **name** (leading `/` stripped); host Entity = "".
- Canonical metrics this phase (Phase 3 renders exactly these):
  - host: `cpu.total`, `cpu.core.<n>`, `load.1m`, `mem.used_pct`, `mem.used_bytes`, `mem.arc_bytes` (when ZFS), `swap.used_pct`, `net.<iface>.rx_bps`/`tx_bps`, `diskio.<dev>.read_bps`/`write_bps`, `temp.<label>.c`, `fan.<label>.rpm`
  - container: `cpu.pct`, `mem.bytes`, `mem.pct`, `net.rx_bps`/`tx_bps`, `io.read_bps`/`write_bps`, `pids`, `cpu.throttled_pct`, `gpu.<engine>.busy_pct`, and PSI when available: `psi.cpu.some_pct`, `psi.io.some_pct`, `psi.mem.some_pct`
  - disk (entity = unraid slot name e.g. `disk1`, `parity`, `cache`): `temp.c`, `spun_up`, `fs.used_bytes`, `fs.free_bytes`, `errors`
  - unraid (entity `array`): `parity.progress_pct`, `parity.speed_bps`, `mover.running`; gpu (entity = pdev): `engine.<name>.busy_pct`
- Per-container **per-device** IO attribution lives in the LIVE ring only (insights need a 15-min window, not history); per-container IO **totals** persist. Host per-device totals persist.
- Tick cadences: docker stats/net/GPU 2s; host 2s; docker inventory 10s; unraid 15s; DiskUsage 5m; full GPU rescan 30s.
- Collector isolation (spec §9): panic recovery per tick, exponential backoff on consecutive errors (1s doubling, cap 5m), re-probe unavailable sources every 60s, one collector's failure never affects another.
- Mount paths inside the container (feature-detected, from the CA template): docker sock `/var/run/docker.sock`, host sysfs `/host/sys`, Unraid state `/unraid`, host procfs = own `/proc` (pid=host). Env overrides: `GANTRY_DOCKER_SOCK`, `GANTRY_HOST_SYS`, `GANTRY_UNRAID_DIR` (for tests/dev).
- Rates: every `*_bps`/`*_pct` from counters is computed from monotonic-tick deltas (previous sample kept per key); first observation emits nothing.
- New dependency allowed: `github.com/docker/docker` (client + api/types) ONLY. Nvidia = exec `nvidia-smi` (runtime-injected), never cgo/dlopen.
- TDD; `go test ./... -race` + `make lint` pristine before every commit; no AI attribution in commits; commit messages exactly as specified.
- Public repo: fixtures captured from Scott's box MUST be anonymized (serials, GUIDs, share names, hostnames) before commit — structure preserved, identifiers replaced.
- ⛔ CHECKPOINT tasks (16, 18) touch Scott's server or need his call — controller gets approval first.

---

### Task 1: Store hardening carry-ins (migrations by prefix, ts index, ring guard)

**Files:**
- Modify: `internal/store/migrate.go`
- Create: `internal/store/migrations/002_ts_indexes.sql`
- Modify: `internal/store/ring.go`
- Test: `internal/store/migrate_test.go`, `internal/store/ring_test.go`

**Interfaces:**
- Consumes: Phase 1 store.
- Produces: migration versions parsed from filename prefix (`002_x.sql` → version 2); `idx_samples_1m_ts` (+ `_10m`, `_1h`) so retention `DELETE ... WHERE ts < ?` and `min(ts)` stop full-scanning; `NewRing` clamps capacity < 1 to 1; and `Live.Evict(kind, entity string)` (in `internal/store/live.go`, under the write lock, deletes every ring whose key matches kind+entity) — Task 6 calls it when a container is removed so ring memory stays O(running containers), spec §9. Test: record two metrics for one entity, `Evict`, `Keys()` no longer contains them while other entities survive.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/migrate_test.go`:

```go
func TestMigrationVersionsComeFromFilenamePrefix(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "g.db"))
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	require.Equal(t, []int{1, 2}, versions) // 001_core.sql, 002_ts_indexes.sql

	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_samples_1m_ts'`).Scan(&n))
	require.Equal(t, 1, n)
}
```

Append to `internal/store/ring_test.go`:

```go
func TestNewRingClampsNonPositiveCapacity(t *testing.T) {
	r := NewRing(0)
	r.Append(Sample{TS: 1, Val: 1}) // must not panic
	require.Equal(t, 1, r.Len())
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run 'TestMigrationVersions|TestNewRingClamps' -v`
Expected: FAIL (no 002 migration; ring panics / count mismatch).

- [ ] **Step 3: Implement**

`internal/store/migrations/002_ts_indexes.sql`:

```sql
CREATE INDEX idx_samples_1m_ts ON samples_1m (ts);
CREATE INDEX idx_samples_10m_ts ON samples_10m (ts);
CREATE INDEX idx_samples_1h_ts ON samples_1h (ts);
```

In `internal/store/migrate.go`, replace the position-based version in `applyMigrations` with prefix parsing:

```go
	for _, name := range names {
		numStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("migration %s: name must be <number>_<desc>.sql", name)
		}
		version, err := strconv.Atoi(numStr)
		if err != nil {
			return fmt.Errorf("migration %s: bad version prefix: %w", name, err)
		}
		// ... existing applied-check/apply/record logic, keyed on this version
	}
```
(add `strconv`, `strings` imports; the rest of the loop body is unchanged).

In `internal/store/ring.go`:

```go
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Sample, capacity)}
}
```

- [ ] **Step 4: Verify green** — `go test ./internal/store/ -race` → PASS (existing idempotency test must still pass — re-run against a DB created pre-002 is not needed; fresh DBs only exist so far).

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(store): filename-prefix migration versions, ts indexes, ring capacity guard"`

---

### Task 2: maintain() extraction + retention wiring + shutdown flush

**Files:**
- Create: `internal/store/maintain.go`
- Modify: `cmd/gantry/main.go`
- Modify: `internal/config/config.go` (nothing — consumed as-is)
- Test: `internal/store/maintain_test.go`

**Interfaces:**
- Consumes: `FlushMinutes`, `DownsampleOnce`, `PruneOnce`, `config.Config.Int`.
- Produces:

```go
// Maintain runs one deep-maintenance pass: flush, then downsample, then prune —
// in that order, all evaluated at the same instant. This ordering IS the I2 fix;
// the test locks it in.
func (s *Store) Maintain(now time.Time, ret Retention) error
// RetentionFromConfig reads retention.r1_hours / r2_days / r3_days / size_cap_mb
// (env GANTRY_RETENTION_*) over DefaultRetention.
func RetentionFromConfig(get func(key string, def int) int) Retention
```
`main.go`: the deep branch becomes one `st.Maintain(time.Now(), ret)` call; `ret` comes from `RetentionFromConfig(cfg.Int)`; after `wg.Wait()`, run a final `st.FlushMinutes(time.Now())` before returning so shutdown stops dropping the last minute.

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The I2 regression guard: samples recorded up to the very end of a 10m window
// must reach the 10m rollup when Maintain runs at the window boundary.
func TestMaintainFlushesBeforeDownsampling(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	base := at("12:00:00")
	_, err := s.FlushMinutes(base) // baseline
	require.NoError(t, err)

	for m := int64(0); m < 10; m++ { // one sample per minute, values 0..9
		s.Record(k, base.Unix()+m*60+30, float64(m))
	}
	// Maintain at the boundary: minute 12:09 is only in the ring at this point.
	require.NoError(t, s.Maintain(at("12:10:05"), DefaultRetention()))

	var avg, max float64
	require.NoError(t, s.DB().QueryRow(`SELECT avg, max FROM samples_10m WHERE ts=?`, base.Unix()).Scan(&avg, &max))
	require.InDelta(t, 4.5, avg, 0.001)
	require.Equal(t, 9.0, max)
}

func TestRetentionFromConfig(t *testing.T) {
	vals := map[string]int{"retention.r1_hours": 24, "retention.size_cap_mb": 128}
	get := func(key string, def int) int {
		if v, ok := vals[key]; ok {
			return v
		}
		return def
	}
	ret := RetentionFromConfig(get)
	require.Equal(t, 24*time.Hour, ret.R1)
	require.Equal(t, DefaultRetention().R2, ret.R2) // untouched keys keep defaults
	require.Equal(t, int64(128<<20), ret.SizeCapBytes)
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/store/ -run 'TestMaintain|TestRetentionFrom' -v` → FAIL.

- [ ] **Step 3: Implement**

`internal/store/maintain.go`:

```go
package store

import "time"

func (s *Store) Maintain(now time.Time, ret Retention) error {
	if _, err := s.FlushMinutes(now); err != nil {
		return err
	}
	if err := s.DownsampleOnce(now); err != nil {
		return err
	}
	return s.PruneOnce(now, ret)
}

func RetentionFromConfig(get func(key string, def int) int) Retention {
	d := DefaultRetention()
	return Retention{
		R1:           time.Duration(get("retention.r1_hours", int(d.R1/time.Hour))) * time.Hour,
		R2:           time.Duration(get("retention.r2_days", int(d.R2/(24*time.Hour)))) * 24 * time.Hour,
		R3:           time.Duration(get("retention.r3_days", int(d.R3/(24*time.Hour)))) * 24 * time.Hour,
		SizeCapBytes: int64(get("retention.size_cap_mb", int(d.SizeCapBytes>>20))) << 20,
	}
}
```

`cmd/gantry/main.go` — in `run()`: build `ret := store.RetentionFromConfig(cfg.Int)` once before the maintenance goroutine; the `deep.C` case body becomes:

```go
			case <-deep.C:
				if err := st.Maintain(time.Now(), ret); err != nil {
					log.Println("maintain:", err)
				}
```
and after `wg.Wait()`, before `return err`:

```go
	if _, ferr := st.FlushMinutes(time.Now()); ferr != nil {
		log.Println("final flush:", ferr)
	}
```

- [ ] **Step 4: Verify green** — full `go test ./... -race` + `make lint`.

- [ ] **Step 5: Commit** — `git commit -am "feat(store): Maintain() locks flush-before-downsample order; retention config wired; shutdown flush"`

---

### Task 3: Collector framework (probe/tick/backoff/panic isolation, sources registry)

**Files:**
- Create: `internal/collect/collect.go`, `internal/collect/runner.go`
- Test: `internal/collect/runner_test.go`

**Interfaces:**
- Consumes: nothing (store passed in by collectors themselves later).
- Produces (every collector task below implements `Collector`; Task 15 wires `Registry` into main + healthz):

```go
type Status struct {
	Available bool
	Detail    string // human hint: why unavailable / what to mount
}
type Collector interface {
	Name() string
	Interval() time.Duration
	Probe(ctx context.Context) Status                 // cheap; called at start and every 60s while unavailable
	Tick(ctx context.Context, now time.Time) error    // one collection pass; only called while available
}
type Registry struct{ /* ... */ }
func NewRegistry() *Registry
func (r *Registry) Add(c Collector)
func (r *Registry) Run(ctx context.Context, wg *sync.WaitGroup) // one goroutine per collector
func (r *Registry) Sources() map[string]string                  // name -> "ok" | detail (for healthz)
```
Runner semantics (all tested): panic in `Tick` is recovered and counted as an error; after N consecutive errors the ticker backs off `1s·2ⁿ` capped at 5m and recovers to the normal interval on the next success; an unavailable collector re-probes every 60s and starts ticking when available; `Run` goroutines exit on ctx cancel and are WaitGroup-tracked.

- [ ] **Step 1: Write the failing test** (drive with a scripted fake collector and short intervals):

```go
package collect

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scripted struct {
	name      string
	avail     atomic.Bool
	ticks     atomic.Int64
	panicOnce atomic.Bool
	failing   atomic.Bool
}

func (s *scripted) Name() string            { return s.name }
func (s *scripted) Interval() time.Duration { return 10 * time.Millisecond }
func (s *scripted) Probe(context.Context) Status {
	if s.avail.Load() {
		return Status{Available: true}
	}
	return Status{Available: false, Detail: "mount missing"}
}
func (s *scripted) Tick(context.Context, time.Time) error {
	s.ticks.Add(1)
	if s.panicOnce.CompareAndSwap(true, false) {
		panic("boom")
	}
	if s.failing.Load() {
		return errors.New("tick failed")
	}
	return nil
}

func TestRunnerTicksAndSurvivesPanic(t *testing.T) {
	c := &scripted{name: "fake"}
	c.avail.Store(true)
	c.panicOnce.Store(true)

	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)

	require.Eventually(t, func() bool { return c.ticks.Load() >= 3 }, 2*time.Second, 5*time.Millisecond,
		"runner must keep ticking after a panic")
	cancel()
	wg.Wait()
	require.Equal(t, "ok", r.Sources()["fake"])
}

func TestRunnerReportsUnavailableWithDetail(t *testing.T) {
	c := &scripted{name: "unraid"} // avail stays false
	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)
	require.Eventually(t, func() bool { return r.Sources()["unraid"] == "mount missing" }, time.Second, 5*time.Millisecond)
	require.Equal(t, int64(0), c.ticks.Load(), "unavailable collectors must not tick")
	cancel()
	wg.Wait()
}

func TestRunnerBacksOffOnConsecutiveErrors(t *testing.T) {
	c := &scripted{name: "flaky"}
	c.avail.Store(true)
	c.failing.Store(true)
	r := NewRegistry()
	r.Add(c)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Run(ctx, &wg)

	time.Sleep(300 * time.Millisecond)
	n := c.ticks.Load()
	require.Less(t, n, int64(12), "backoff must slow a persistently failing collector well below the 10ms cadence (30 ticks)")
	require.GreaterOrEqual(t, n, int64(1))
	cancel()
	wg.Wait()
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/collect/ -v` → FAIL, undefined.

- [ ] **Step 3: Implement**

`internal/collect/collect.go` holds the types above verbatim. `internal/collect/runner.go`:

```go
package collect

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	reprobeEvery = 60 * time.Second
	backoffCap   = 5 * time.Minute
)

type entry struct {
	c      Collector
	mu     sync.Mutex
	status Status
}

type Registry struct {
	mu      sync.RWMutex
	entries []*entry
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Add(c Collector) {
	r.mu.Lock()
	r.entries = append(r.entries, &entry{c: c})
	r.mu.Unlock()
}

func (r *Registry) Sources() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.entries))
	for _, e := range r.entries {
		e.mu.Lock()
		if e.status.Available {
			out[e.c.Name()] = "ok"
		} else {
			out[e.c.Name()] = e.status.Detail
		}
		e.mu.Unlock()
	}
	return out
}

func (r *Registry) Run(ctx context.Context, wg *sync.WaitGroup) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		wg.Add(1)
		go func(e *entry) {
			defer wg.Done()
			r.runOne(ctx, e)
		}(e)
	}
}

func (r *Registry) runOne(ctx context.Context, e *entry) {
	setStatus := func(s Status) { e.mu.Lock(); e.status = s; e.mu.Unlock() }
	consecutive := 0

	setStatus(e.c.Probe(ctx))
	for {
		e.mu.Lock()
		available := e.status.Available
		e.mu.Unlock()

		var wait time.Duration
		switch {
		case !available:
			wait = reprobeEvery
		case consecutive > 0:
			wait = backoff(consecutive, e.c.Interval())
		default:
			wait = e.c.Interval()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		if !available {
			setStatus(e.c.Probe(ctx))
			continue
		}
		if err := safeTick(ctx, e.c); err != nil {
			consecutive++
			if consecutive == 1 || consecutive%10 == 0 {
				log.Printf("collector %s: %v (consecutive=%d)", e.c.Name(), err, consecutive)
			}
		} else {
			consecutive = 0
		}
	}
}

func backoff(consecutive int, base time.Duration) time.Duration {
	d := time.Second
	for i := 1; i < consecutive; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	if d < base {
		return base
	}
	return d
}

func safeTick(ctx context.Context, c Collector) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return c.Tick(ctx, time.Now())
}
```
(add `fmt` import; note `Tick` receives `time.Now()` here — collectors that need the tick instant take it from the argument).

- [ ] **Step 4: Verify green** — `go test ./internal/collect/ -race -v` (the panic test is the important one).

- [ ] **Step 5: Commit** — `git commit -am "feat(collect): collector framework with probe, backoff, panic isolation, sources registry"`

---

### Task 4: Rate helper + host CPU/mem/load collector

**Files:**
- Create: `internal/collect/rates.go`, `internal/collect/host/host.go`, `internal/collect/host/proc.go`
- Create fixtures: `internal/collect/host/testdata/proc_stat.txt`, `meminfo.txt`, `loadavg.txt`, `arcstats.txt`
- Test: `internal/collect/rates_test.go`, `internal/collect/host/proc_test.go`

**Interfaces:**
- Consumes: framework (Task 3), `store.MetricSink`.
- Produces:

```go
// internal/collect — shared by every counter-based collector
type RateTracker struct{ /* map[string]{prev float64, prevTS time.Time} */ }
func NewRateTracker() *RateTracker
// Rate returns (delta/seconds, true) for a monotonically-increasing counter,
// or (0, false) on first observation or counter reset (delta < 0).
func (r *RateTracker) Rate(key string, now time.Time, counter float64) (float64, bool)

// internal/collect/host
func New(sink store.MetricSink, procRoot, sysRoot string) *Collector // implements collect.Collector, Name "host", Interval 2s
// pure parsers (fixture-tested):
func parseProcStat(r io.Reader) (total cpuTimes, perCore []cpuTimes, err error) // user/nice/system/idle/iowait/irq/softirq/steal
func parseMeminfo(r io.Reader) (memTotal, memAvailable, swapTotal, swapFree uint64, err error) // kB
func parseLoadavg(r io.Reader) (load1 float64, err error)
func parseArcstats(r io.Reader) (arcSizeBytes uint64, ok bool)
```
Tick emits: `cpu.total` + `cpu.core.<n>` (busy% from cpuTimes deltas: 100·(1−(Δidle+Δiowait)/Δtotal)), `mem.used_pct` + `mem.used_bytes` (total−available), `mem.arc_bytes` when `/proc/spl/kstat/zfs/arcstats` exists, `swap.used_pct`, `load.1m`, and `uptime_s` (first float in `/proc/uptime`). First tick emits only gauges; CPU rates start on the second tick.

`RateTracker` computes windows with `now.Sub(prev)` on `time.Time` values (Go subtracts on the monotonic clock when both carry it) — never with `Unix()` arithmetic — so rates survive NTP steps. The wall-clock flusher/cascade limitation (spec §9) remains known deferred debt, unchanged by this phase.

- [ ] **Step 1: Fixtures** — realistic `/proc/stat` (2 cores + total, two snapshots inline in the test for delta math), `meminfo` with MemTotal/MemAvailable/SwapTotal/SwapFree, `loadavg` `0.52 0.58 0.59 2/1024 12345`, `arcstats` with a `size 4 8589934592` line.

- [ ] **Step 2: Failing tests** — table-driven parser tests asserting exact numbers from fixtures; a RateTracker test (first call false; 100→160 over 2s → 30/s; reset 160→10 → false then resumes); a Tick-level test with a fake sink and stubbed proc files in a temp dir (write fixture snapshot A, Tick, overwrite with snapshot B, Tick, assert `cpu.total` ≈ expected busy% and `mem.used_pct` exact).

- [ ] **Step 3: Implement** — parsers are line-scanners over the fixtures' exact formats (no regex); `Collector.Tick` reads `procRoot+"/stat"` etc. so tests point at temp dirs and production passes `/proc`. `Probe` checks `procRoot+"/stat"` readable.

- [ ] **Step 4: Verify green** — `go test ./internal/collect/... -race`.

- [ ] **Step 5: Commit** — `git commit -am "feat(host): cpu/mem/load/arc collector with fixture-tested parsers and rate tracker"`

---

### Task 5: Host network + disk IO + hwmon collector half

**Files:**
- Modify: `internal/collect/host/host.go`
- Create: `internal/collect/host/netdev.go`, `internal/collect/host/diskstats.go`, `internal/collect/host/hwmon.go`
- Create fixtures: `testdata/net_dev.txt`, `testdata/diskstats.txt`, plus an hwmon temp-dir builder in the test
- Test: `internal/collect/host/netdev_test.go`, `diskstats_test.go`, `hwmon_test.go`

**Interfaces:**
- Consumes: `RateTracker` (Task 4). Host net counters come from `/proc/1/net/dev` (host init netns — pid=host makes this the host view; spec §4.2).
- Produces:

```go
func parseNetDev(r io.Reader) (map[string]ifCounters, error) // iface -> rx/tx bytes; caller filters
func filteredIfaces(all map[string]ifCounters) map[string]ifCounters // drops lo, veth*, virbr*, br-*, docker0, tap*
func parseDiskstats(r io.Reader) (map[string]diskCounters, error) // dev -> sectors read/written (×512 = bytes); keeps sd*, nvme*n*, md* only (not partitions)
// DeviceMap: major:minor -> device name, rebuilt each tick from diskstats fields 1-3.
// Exported for the docker collector's per-device io.stat attribution (Task 8):
func (c *Collector) DeviceName(majMin string) (string, bool)
func scanHwmon(sysRoot string) []hwmonReading // walks /host/sys/class/hwmon/*/temp*_input + fan*_input with label fallbacks
```
Tick emits `net.<iface>.rx_bps`/`tx_bps`, `diskio.<dev>.read_bps`/`write_bps`, `temp.<label>.c`, `fan.<label>.rpm`. Partition filtering: a device name that is a prefix of another with a trailing digit (`sda` vs `sda1`, `nvme0n1` vs `nvme0n1p1`) — keep whole-device rows only (match `sd[a-z]+$`, `nvme\d+n\d+$`, `md\d+p?$`).

- [ ] **Step 1: Fixtures** — real-shaped `/proc/net/dev` (lo, eth0, veth pair, docker0), `/proc/diskstats` (sda, sda1, nvme0n1, nvme0n1p1, md1) with known sector counts; hwmon tree built with os.MkdirAll/WriteFile in the test (`hwmon0/name`=coretemp, `temp1_input`=45000, `temp1_label`=Package id 0; `hwmon1/fan1_input`=1200).

- [ ] **Step 2: Failing tests** — parseNetDev exact byte counts + filter drops veth/docker0/lo; parseDiskstats keeps sda/nvme0n1/md1, drops partitions, sectors×512 math; DeviceName resolves "8:0"→"sda"; hwmon labels resolve (`coretemp Package id 0` → `temp.coretemp_package_id_0.c` slug — lowercase, spaces→underscores).

- [ ] **Step 3: Implement** — netdev path is `procRoot + "/1/net/dev"` in production (document why in one comment: host init netns). Diskstats fields: 3=dev name, 6=sectors read, 10=sectors written (1-indexed per kernel doc); major=1, minor=2 for the DeviceMap.

- [ ] **Step 4: Verify green**; **Step 5: Commit** — `git commit -am "feat(host): host netdev, diskstats with device map, hwmon temps and fans"`

---

### Task 6: Docker collector — client, inventory, registry, events

**Files:**
- Create: `internal/collect/docker/docker.go`, `internal/collect/docker/registry.go`
- Test: `internal/collect/docker/registry_test.go` (pure), `internal/collect/docker/docker_test.go` (integration, build-tagged)

**Interfaces:**
- Consumes: framework, `store.MetricSink`, `store.AppendEvent` (via a narrow `EventSink interface{ AppendEvent(store.Event) (int64, error) }`), docker SDK (`go get github.com/docker/docker@latest && go mod tidy` — the one allowed dep).
- Produces:

```go
func New(sink store.MetricSink, events EventSink, sockPath string) *Collector // Name "docker", Interval 2s
// Registry: the id→meta map every other collector consumes.
type Meta struct {
	ID, Name, Image, State, Health string
	Pid                            int
	StartedAt                      time.Time
	HostNet                        bool
	RestartCount                   int
}
func (c *Collector) Lookup(containerID string) (Meta, bool)   // for GPU attribution (Task 9)
func (c *Collector) Running() []Meta                          // snapshot, name-sorted
```
Behavior: `Probe` pings the socket (client.Ping). Inventory refresh every 10s (ContainerList(All) + ContainerInspect for Pid/Health/HostNet) plus immediately on any docker **event**; the events goroutine (started on first successful Probe, restarted with backoff by the collector itself on stream error) translates start/die/oom/health_status events into `store.Event{Kind: "container.start"|"container.die"|"container.oom"|"container.health", Entity: name, Severity, Detail}`. Name normalization strips the leading `/`. State/health/restart-count changes detected during inventory also emit events (belt for missed stream gaps). On a container **remove/destroy** event (or a name vanishing from inventory), call the injected `evict func(kind, entity string)` (wired to `store.Live.Evict` in Task 15) with kind "container" — dead containers' rings must not linger (spec §9).

- [ ] **Step 1: Failing tests** — registry_test (pure): apply a scripted sequence of inventory snapshots + events to the registry struct and assert Lookup/Running contents, name normalization, event emission on health flip (fake EventSink capture). docker_test.go behind `//go:build dockertest` tag: against the real local daemon — start a tiny container (`docker run -d --rm alpine:3 sleep 30` via the SDK), assert it appears in Running() with a Pid > 0, stop it, assert a `container.die` event lands. CI runs `go test -tags dockertest ./internal/collect/docker/` in a follow-up CI edit (Task 15); locally it runs when a daemon is present.

- [ ] **Step 2-4:** standard TDD cycle. The SDK client is constructed with `client.WithHost("unix://"+sockPath)`, `client.WithAPIVersionNegotiation()`.

- [ ] **Step 5: Commit** — `git commit -am "feat(docker): client, container registry, inventory poll and event stream"`

---

### Task 7: Docker stats fast path — cgroup v2 readers

**Files:**
- Create: `internal/collect/docker/cgroupv2.go`
- Create fixtures: `internal/collect/docker/testdata/cpu.stat`, `memory.stat`, `memory.current`, `io.stat`, `pids.current`
- Test: `internal/collect/docker/cgroupv2_test.go`

**Interfaces:**
- Consumes: registry (Task 6), `RateTracker`, host `DeviceName` (Task 5, injected as `func(string) (string, bool)`).
- Produces:

```go
// readCgroupStats reads one container's cgroup dir (e.g. /host/sys/fs/cgroup/docker/<id>).
type cgStats struct {
	CPUUsageUsec, ThrottledUsec, NrThrottled uint64
	MemCurrent, MemInactiveFile              uint64
	Pids                                     uint64
	IO                                       map[string]ioCounters // maj:min -> rbytes/wbytes
}
func readCgroupStats(dir string) (cgStats, error)
```
Tick math (per running container, id from registry): `cpu.pct` = ΔCPUUsageUsec / Δwall_usec × 100 (whole-host normalized); `cpu.throttled_pct` = ΔThrottledUsec/Δwall × 100; `mem.bytes` = MemCurrent − MemInactiveFile (docker-stats definition, spec §4.1); `mem.pct` vs host MemTotal (taken from a `HostTotals` func injected from the host collector — simple atomic value it publishes); `pids`; `io.read_bps`/`write_bps` = summed device deltas; per-device rates recorded to the LIVE ring only as `io.<devname>.read_bps` (per Global Constraints — persisted totals, live-only per-device: the flusher must skip these; mechanism: metric names prefixed `live:` are skipped by `FlushMinutes` — add that skip + a one-line test to `internal/store/flush.go` in THIS task, metric form `live:io.<dev>.read_bps`).

- [ ] **Step 1: Fixtures** — real cgroup v2 file shapes: `cpu.stat` (matches the spike capture: usage_usec/user_usec/system_usec/nr_throttled/throttled_usec...), `memory.stat` (~40 lines incl. `inactive_file 123456789`), `io.stat` (`8:0 rbytes=1024000 wbytes=2048000 rios=100 wios=200 dbytes=0 dios=0`, plus a second device line), `memory.current`/`pids.current` single numbers.

- [ ] **Step 2: Failing tests** — readCgroupStats exact values from a temp-dir cgroup; the delta math via two synthetic reads through RateTracker; the `live:` flusher-skip test in store.

- [ ] **Step 3-4:** implement + green (`go test ./... -race` — store test included).

- [ ] **Step 5: Commit** — `git commit -am "feat(docker): cgroup v2 stats fast path with per-device live-only io attribution"`

---

### Task 8: Docker stats fallback (API) + per-container network

**Files:**
- Create: `internal/collect/docker/apistats.go`, `internal/collect/docker/net.go`
- Test: `internal/collect/docker/net_test.go` (fixture: reuse Task 5's net_dev.txt shape), apistats covered by the dockertest-tagged integration test

**Interfaces:**
- Consumes: registry Pids, `parseNetDev` (exported from host package or duplicated as a tiny local parser — EXPORT it from host as `host.ParseNetDev` in this task; update Task 5's file accordingly).
- Produces: per-container `net.rx_bps`/`tx_bps` from `/proc/<pid>/net/dev` (sum non-lo ifaces inside the container's netns); containers with `HostNet: true` are skipped (spec: labeled, not attributed). Fallback: when the cgroup dir read fails for a container (v1 box or masked path), one-shot `ContainerStatsOneShot` from the SDK maps into the same cgStats shape (CPU from `cpu_stats`, mem from `usage - inactive_file`, io from `blkio_stats`); selection is per-container, automatic, logged once.

- [ ] **Steps:** TDD on the netns read (temp proc dir with `<pid>/net/dev` fixture); fallback mapping gets a unit test on the types.StatsJSON→cgStats conversion with a hand-built StatsJSON literal. Commit: `git commit -am "feat(docker): per-container network via netns procfs and API stats fallback"`

---

### Task 9: GPU collector — fdinfo walker with container attribution

**Files:**
- Create: `internal/collect/gpu/collector.go`, `internal/collect/gpu/walker.go`
- Test: `internal/collect/gpu/walker_test.go` (temp-dir /proc tree built in test)

**Interfaces:**
- Consumes: `ParseFDInfo` (Phase 1), `cgroup.ContainerID` (Phase 1), docker `Lookup` (Task 6, injected as `func(id string) (name string, ok bool)`), `RateTracker`.
- Produces: `New(sink store.MetricSink, procRoot string, lookup func(string) (string, bool)) *Collector` — Name "gpu", Interval 2s. Mechanics (spec §4.4 + spike S1):
  - Full scan every 30s: walk `procRoot/*/fdinfo/*`, collect DRM clients (dedupe by `drm-client-id` — multiple fds of one client count once), remember (pid, fd path, client-id, driver).
  - 2s tick: re-read known client fdinfo files; engine busy-% per client = Δ`drm-engine-<name>` ns / Δwall ns × 100 (i915/amdgpu ns path — xe cycles deferred until seen in the wild; unknown drm-engine units are skipped with a once-log).
  - Attribution: pid → `/proc/<pid>/cgroup` → ContainerID → `lookup` → container name; **unmapped clients aggregate into the host bucket** (spike finding: real host-side clients exist). Dead clients (read fails) drop out until the next full scan.
  - Emits: per-container `gpu.<engine>.busy_pct` (summed across that container's clients), gpu-entity `engine.<engine>.busy_pct` (all clients incl. host bucket), entity = `drm-pdev` value (falls back to "gpu0").
- [ ] **Steps:** build a fake /proc tree in-test: two pids with fdinfo files (one whose cgroup file maps to container id `aaaa…` and one host pid), tick twice with hand-advanced counter values written between ticks, assert per-container and total busy percentages exactly; dedupe test (same client-id via two fds). Then implement, green, commit: `git commit -am "feat(gpu): fdinfo walker with per-container attribution and host bucketing"`

---

### Task 10: PSI + pressure collectors (two-tier, feature-detected)

**Files:**
- Create: `internal/collect/pressure/pressure.go`
- Test: `internal/collect/pressure/pressure_test.go`

**Interfaces:**
- Consumes: registry Running() (injected as `func() []docker.Meta`), cgroup dir layout from Task 7.
- Produces: parses `some avg10=1.23 avg60=... total=...` lines from `/proc/pressure/{cpu,io,memory}` (host series `psi.<res>.some_pct`) and each container's cgroup `{cpu,io,memory}.pressure` (`psi.<res>.some_pct` on the container). `Probe` returns unavailable with Detail `"PSI disabled — add psi=1 to the syslinux append line to enable (optional; used by insights)"` when `/proc/pressure` is absent — this string is what the UI hint will show (spec §16 two-tier).
- [ ] **Steps:** fixture `some avg10=0.00 avg60=1.11 avg300=2.22 total=12345678` + `full ...` second line (parse `some avg10` only); TDD; commit `git commit -am "feat(pressure): PSI collectors with psi=1 enablement hint (insights tier 1)"`

---

### Task 11: Unraid collector — tolerant INI + var.ini (array/parity) interpretation

**Files:**
- Create: `internal/collect/unraid/ini.go`, `internal/collect/unraid/unraid.go`, `internal/collect/unraid/var.go`
- Create fixtures: `internal/collect/unraid/testdata/var_started.ini`, `var_parity_running.ini`, `var_stopped.ini` (hand-authored now; REPLACED by anonymized real captures in Task 16)
- Test: `internal/collect/unraid/ini_test.go`, `var_test.go`

**Interfaces:**
- Consumes: framework, sink, EventSink.
- Produces:

```go
// ParseINI: tolerant emhttp dialect — `key="value"` lines, optional [section] headers,
// unknown lines skipped silently, values unquoted.
func ParseINI(r io.Reader) (map[string]map[string]string, error) // "" section for headerless keys
type ArrayState struct {
	State          string  // mdState: STARTED | STOPPED | ...
	ParityRunning  bool    // mdResyncPos > 0
	ParityProgress float64 // pos/size*100
	ParitySpeedBps float64 // mdResyncSpeed (KB/s units) * 1024
	Version        string
}
func interpretVar(kv map[string]map[string]string) ArrayState
```
`Collector` (Name "unraid", Interval 15s, Probe = `<dir>/var.ini` readable): emits `parity.progress_pct`, `parity.speed_bps`, gauge `mover.running` (Task 12), and **events on transitions** (`array.state` when mdState changes, `parity.start`/`parity.finish` on ParityRunning edges — finish Detail carries final progress and duration; previous state kept in the collector).
- [ ] **Steps:** TDD parsers on fixtures (started/stopped/mid-parity with real key names `mdState`, `mdResyncPos`, `mdResyncSize`, `mdResyncSpeed`, `version`); transition-event test via two interpretVar states through the collector's edge detector. Commit: `git commit -am "feat(unraid): tolerant emhttp ini parser and array/parity state with transition events"`

---

### Task 12: Unraid collector — disks.ini, shares.ini, mover, UPS detect

**Files:**
- Create: `internal/collect/unraid/disks.go`, `internal/collect/unraid/shares.go`, `internal/collect/unraid/mover.go`
- Fixtures: `testdata/disks.ini` (parity + 3 data + cache + an empty slot; keys: `name`, `device`, `status` DISK_OK/DISK_NP, `temp` (number or `*` when spun down), `numErrors`, `sizeSb`, `fsSize`, `fsFree`, `spundown` 0/1, `rotational`), `testdata/shares.ini`
- Test: `internal/collect/unraid/disks_test.go`, `shares_test.go`, `mover_test.go`

**Interfaces:**
- Produces: per-disk (Kind "disk", Entity = slot name): `temp.c` (omit sample when spun down/`*` — absence IS the signal, spun-down disks must not chart as 0°), `spun_up` 0/1, `fs.used_bytes` (fsSize−fsFree, both KB units → bytes), `fs.free_bytes`, `errors` (counter; the alert engine diffs it in Phase 4; also emit `disk.errors` event on increment NOW — cheap and useful); shares: total used bytes per share as `unraid`-kind gauge `share.<name>.used_bytes` (15s is fine, values move slowly); mover: `mover.running` 0/1 via host PID table scan for comm == "mover" (`procRoot/*/comm`); UPS: probe `<dir>/ups.ini` — if present emit basic `ups.charge_pct`/`ups.load_pct`, else silently absent (best-effort per spec).
- [ ] **Steps:** TDD each parser off fixtures incl. the spun-down `temp=*` case and empty slot skip (status DISK_NP); mover test against a fake proc tree. Commit: `git commit -am "feat(unraid): per-disk stats with spin-aware temps, shares, mover detection, ups best-effort"`

---

### Task 13: Docker disk usage poll + Nvidia via nvidia-smi (best-effort)

**Files:**
- Create: `internal/collect/docker/diskusage.go`, `internal/collect/gpu/nvidia.go`
- Test: `internal/collect/gpu/nvidia_test.go` (parser on canned output), diskusage via dockertest tag

**Interfaces:**
- Produces: every 5m (own tiny collector "docker-disk", or a modulo counter inside the docker collector — implement as separate Collector, simplest): SDK `DiskUsage` → gauges `docker.images_bytes`, `docker.containers_bytes`, `docker.volumes_bytes` (unraid-kind entity "docker"). Nvidia: `Probe` = exec.LookPath("nvidia-smi"); Tick = `nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader,nounits` + `--query-compute-apps=pid,used_memory --format=csv,noheader,nounits`; per-process pid → same cgroup→container mapping; emits gpu-entity util + per-container `gpu.nvidia.busy_pct` approximation (process SM share unavailable via CSV — document plainly: per-container Nvidia v1 = VRAM per process + presence, utilization host-level; refine in a later phase with NVML if demanded). **Hardware-untested** (no Nvidia box available) — parser fixtures come from documented nvidia-smi output; the collector logs a one-line "hardware-unvalidated" note on first tick. Spec §4.4 wording updated by this task (see step below).
- [ ] **Steps:** TDD the CSV parsers; spec edit: in §4.4 replace "Gantry dlopens `libnvidia-ml.so` if present" with "Gantry execs the runtime-injected `nvidia-smi` (CSV queries) if present — dlopen would require cgo or an FFI dependency; exec keeps the static build" and note per-container Nvidia = VRAM+presence in v1. Commit: `git commit -am "feat: docker disk usage poll and best-effort nvidia-smi collector; spec: nvidia via exec"`

---

### Task 14: Snapshot API — /api/live/snapshot + /api/containers

**Files:**
- Create: `internal/server/api_snapshot.go`
- Modify: `internal/server/server.go` (routes + Options gains `Sources func() map[string]string` and `Registry *docker.Collector`? — NO: keep server decoupled; Options gains `Sources func() map[string]string` and `Snapshot func() SnapshotDTO` built in cmd wiring)
- Test: `internal/server/api_snapshot_test.go`

**Interfaces:**
- Consumes: `store.Live` (Keys/Latest), docker Running() — both accessed through a `Snapshot func() SnapshotDTO` closure assembled in main (server stays store-shape-agnostic).
- Produces:

```go
type SnapshotDTO struct {
	TS         int64                         `json:"ts"`
	Host       map[string]float64            `json:"host"`       // metric -> latest
	Containers map[string]ContainerDTO       `json:"containers"` // name -> meta+metrics
	Disks      map[string]map[string]float64 `json:"disks"`
	Unraid     map[string]float64            `json:"unraid"`
	GPU        map[string]map[string]float64 `json:"gpu"`
}
type ContainerDTO struct {
	State   string             `json:"state"`
	Health  string             `json:"health"`
	Image   string             `json:"image"`
	Metrics map[string]float64 `json:"metrics"`
}
```
`SnapshotDTO` also carries `UnraidVersion string `json:"unraid_version"`` (strings don't fit the float maps) — Task 11's collector exposes `Version() string` for it. Routes: `GET /api/live/snapshot` (the DTO; this becomes the SSE seed frame in Phase 3 — not throwaway), `GET /api/containers` (Running() metas). healthz `sources` now serves the real registry map.
- [ ] **Steps:** TDD with a hand-assembled snapshot closure; verify JSON shape + content-type; healthz sources test updated. Commit: `git commit -am "feat(server): live snapshot and containers endpoints; healthz reports real sources"`

---

### Task 15: Wire it all — main assembly, read pool, CI dockertest

**Files:**
- Modify: `cmd/gantry/main.go`, `internal/store/store.go` (read handle), `.github/workflows/ci.yml`
- Test: `cmd/gantry/main_test.go` (extend the existing run() test)

**Interfaces:**
- Consumes: everything above.
- Produces: `run()` builds the registry: host (procfs `/proc`, sysfs from `GANTRY_HOST_SYS` default `/host/sys`), docker (+disk), unraid (`GANTRY_UNRAID_DIR` default `/unraid`), gpu, pressure, selfstat — all always added (probe decides availability); fake generator now runs ONLY when `GANTRY_FAKE_DATA=1` (unchanged) and real collectors are simply unavailable on a dev Mac (sources map shows the hints — the degradation model working as designed). Wiring adapters live here, in main: GPU's lookup = `func(id string) (string, bool) { m, ok := dc.Lookup(id); return m.Name, ok }`; docker's evict = `st.Live().Evict`; docker's per-container `mem.pct` denominator = the host collector's published `MemTotal()` (an atomic uint64 the host collector updates each tick, 0 = emit no mem.pct); pressure's container list = `dc.Running`. Store gains a read handle — a second `sql.Open` on the same path with `SetMaxOpenConns(4)`, exposed as `Store.ReadDB() *sql.DB`, used by future Phase 3 queries; created in Open, closed in Close (WAL allows concurrent readers; the write handle stays MaxOpenConns(1)). All maintenance queries move to `*Context` variants using the run context (uncancellable-prune fix). CI: add a `dockertest` job step running `go test -tags dockertest ./internal/collect/docker/` (ubuntu runners have a daemon).
- [ ] **Steps:** extend TestRunServesHealthzAndShutsDown to assert healthz now carries a non-empty `sources` map with expected keys (all "unavailable" details on the test box — that's correct behavior); store read-pool test (both handles usable concurrently under -race); TDD; commit: `git commit -am "feat: assemble real collectors in main with read pool and dockertest CI"`

---

### Task 16: ⛔ CHECKPOINT — capture + anonymize real fixtures from Scott's box

**Files:**
- Modify: fixtures under `internal/collect/unraid/testdata/`, `internal/collect/gpu/testdata/`
- Create: `docs/superpowers/fixtures.md` (what was captured, what was scrubbed)

- [ ] **Step 1: ⛔ Scott's go-ahead** to read (read-only) `/var/local/emhttp/{var,disks,shares}.ini` and a couple of live `/proc/<pid>/fdinfo` files over SSH.
- [ ] **Step 2: Capture** — `ssh root@192.168.1.50 'cat /var/local/emhttp/var.ini'` etc. into the SDD workspace (never straight into the repo).
- [ ] **Step 3: Anonymize** — replace disk serials/ids (`device`, `id` keys), share names, GUID/regcheck fields, hostname with structurally-identical placeholders; keep every key name and numeric shape. Diff against the hand-authored fixtures; where reality disagrees (extra keys, different value forms), update parsers + tests to match reality (TDD: add the real-shape case first).
- [ ] **Step 4:** replace/augment fixtures, run `go test ./... -race`, note discrepancies found in `docs/superpowers/fixtures.md`, commit: `git commit -am "test: anonymized real-box fixtures for unraid parsers"`

---

### Task 17: Self-footprint gauge (the "receipt")

**Files:**
- Create: `internal/collect/selfstat/selfstat.go`
- Test: `internal/collect/selfstat/selfstat_test.go`

**Interfaces:**
- Produces: tiny collector (Interval 10s) reading its own `/proc/self/stat` (utime+stime → cpu.pct via RateTracker) and `/proc/self/statm` (RSS pages × page size) → host-kind metrics `gantry.cpu_pct`, `gantry.rss_bytes`. Powers spec §2's budget check (Task 18 reads it) and the Settings-page receipt in Phase 3.
- [ ] **Steps:** TDD against fixture stat/statm lines; commit `git commit -am "feat(selfstat): gantry's own cpu/rss as first-class metrics"`

---

### Task 18: ⛔ CHECKPOINT — on-box deployment + live validation

**Files:**
- Create: `docs/superpowers/phase-2-validation.md`

- [ ] **Step 1: ⛔ Scott's go-ahead** for: building the image on the box from the repo, running gantry with the full template flags (this is the first real deploy — port 8380), and starting a short transcode (or Scott plays one) for GPU validation.
- [ ] **Step 2: Deploy** — on the box: `docker build -t gantry:phase2 https://github.com/smidley/gantry.git#main` then run with all template mounts/flags (no fake data), `-p 8380:8380`, temp `/config` volume.
- [ ] **Step 3: Validate, capture evidence into the doc:**
  - `curl /api/healthz` → sources all "ok" except pressure (PSI hint text) and nvidia (absent).
  - `curl /api/live/snapshot` → 40 containers with real cpu/mem/net/io; disks with temps + spin states matching the Unraid UI; parity/mover idle states sane.
  - **GPU e2e (the Phase 1 carry-in):** with a transcode running, snapshot shows `gpu.video.busy_pct > 0` attributed to the right container name (jellyfin/Tunarr/Optimisarr) — this closes the PID→container validation gap. If attribution misses, capture `/proc/<pid>/cgroup` of the ffmpeg pid and fix the extractor with a real fixture before proceeding.
  - **Budgets (spec §2, adjusted for 40 containers):** `gantry.cpu_pct` ≤ ~2% of one core sustained, `gantry.rss_bytes` ≤ 100MB (pro-rated from the 30-container budget; record actuals).
  - HEALTHCHECK reaches `healthy` on the box; container survives 30+ min unattended (no restart, no error-log spam).
- [ ] **Step 4:** record verdicts + numbers, fix anything found (TDD for code fixes), commit: `git commit -am "docs: phase 2 on-box validation results"`

---

## Phase 2 exit criteria

- All collectors green on the real box with real data visible via `/api/live/snapshot`; sources map honest on both the box (mostly ok) and a bare dev machine (mostly hints).
- GPU per-container attribution validated live (or the miss diagnosed and fixed with a real fixture).
- Footprint receipt within budget, recorded.
- Fixtures for unraid parsers are anonymized real captures.
- `go test ./... -race` green incl. dockertest job in CI.

**Next:** Phase 3 plan (SSE + the full Svelte UI) — the snapshot DTO from Task 14 is its seed frame.
