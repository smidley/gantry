# Gantry Phase 1: Foundation & Spikes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A runnable Gantry container (scratch image, healthcheck, fake-data dashboard feed) with the complete, fully-tested storage backbone, plus verified spike results (S1 fdinfo / S2 notify / S3 cgroup) recorded from Scott's Unraid box.

**Architecture:** Single Go binary. `internal/store` owns the live ring buffer, SQLite tiers (1m/10m/1h), downsampling, pruning, events, and settings — everything downstream (collectors, API, alerts) talks to it through small interfaces defined here. A fake-data generator exercises the exact `MetricSink` path real collectors will use in Phase 2. Spike probes are a separate throwaway binary (`cmd/spikeprobe`) that wraps *production* parsers, so spike verification doubles as parser validation.

**Tech Stack:** Go ≥1.25 (stdlib HTTP, `go:embed`), `modernc.org/sqlite` (pure Go, CGO-free), `github.com/stretchr/testify` (tests only). No frontend toolchain in this phase (placeholder page only).

**Spec:** `docs/superpowers/specs/2026-08-25-gantry-design.md`

## Phase roadmap (spec §→ phase coverage)

| Phase | Spec sections | Plan |
|---|---|---|
| 1 (this) | §3 posture/skeleton, §5 storage/retention/settings, §9 SQLite integrity + bounds (partial), §12 Dockerfile/CI (initial), §13 spikes | this file |
| 2 | §4 collectors (docker/host/unraid/gpu), §9 collector isolation | written after Phase 1 + spike results |
| 3 | §6 API/SSE, §7 UI (all 9 views) | written after Phase 2 |
| 4 | §8 alerting, §10 security polish, §11 pre-release validation, §12 CA release | written after Phase 3 |
| 5 | §16 cross-container impact insights (engine + UI; its data enablers land in Phase 2) | written after launch-scope decision |

## Global Constraints

- Go module path: `github.com/smidley/gantry`. License: MIT.
- `CGO_ENABLED=0` always; final image is `scratch`, linux/amd64 only.
- Web UI port default **8380**; env override prefix `GANTRY_` (e.g. `GANTRY_PORT`, `GANTRY_FAKE_DATA`).
- Retention defaults: live ring 2s×15min (450 samples); R1 1min/48h; R2 10min/30d; R3 1h/13 months; DB hard cap 512MB.
- SQLite: WAL, `synchronous=NORMAL`, `busy_timeout=5000`, single writer (`SetMaxOpenConns(1)` this phase).
- Timestamps in store: unix **seconds**, aligned to tier resolution.
- TDD: every code task = failing test → minimal code → pass → commit. Run `go test ./...` before every commit.
- No new dependencies beyond `modernc.org/sqlite` and `testify` without a note in the task.
- Outward-facing actions (GitHub repo creation, anything touching Scott's server) are **gated tasks** — marked ⛔ CHECKPOINT; the orchestrator gets Scott's go-ahead first.

---

### Task 1: Repo scaffold

**Files:**
- Create: `go.mod`, `.gitignore`, `LICENSE`, `README.md`, `Makefile`
- Create (dirs kept by files in later tasks): `cmd/gantry/`, `internal/store/`, `docs/superpowers/plans/` (exists)

**Interfaces:**
- Consumes: nothing.
- Produces: the module `github.com/smidley/gantry`; `make build/test/lint/fmt` targets every later task uses.

- [ ] **Step 1: Verify toolchain**

Run: `go version`
Expected: go1.25 or newer. If missing/old: `brew install go`.

- [ ] **Step 2: Create go.mod and layout**

```bash
cd /Users/scottbrant/Documents/gantry
go mod init github.com/smidley/gantry
```

Create `.gitignore`:

```gitignore
/gantry
/spikeprobe
*.db
*.db-wal
*.db-shm
/config/
dist/
node_modules/
.DS_Store
```

Create `LICENSE` with the standard MIT text, copyright line: `Copyright (c) 2026 Scott Brant`.

Create `README.md`:

```markdown
# Gantry

A Docker and server monitor built for Unraid. One container. Zero configuration.

> Status: pre-release, under active development.

Design spec: [docs/superpowers/specs/2026-08-25-gantry-design.md](docs/superpowers/specs/2026-08-25-gantry-design.md)
```

Create `Makefile`:

```makefile
VERSION ?= dev

.PHONY: build test lint fmt docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o gantry ./cmd/gantry

test:
	go test ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

docker:
	docker build --build-arg VERSION=$(VERSION) -t gantry:dev .
```

- [ ] **Step 3: Verify**

Run: `make lint` (passes trivially — no Go files yet is fine; `go vet ./...` on an empty module prints a "no packages" style note, acceptable) and `git status` shows the new files.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "chore: scaffold module, Makefile, license"
```

---

### Task 2: Store — migration runner + core schema

**Files:**
- Create: `internal/store/migrate.go`, `internal/store/migrations/001_core.sql`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.OpenDB(path string) (*sql.DB, error)` — opens SQLite with pragmas and applies all embedded migrations idempotently. Every later store task builds on the schema below.

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenDBCreatesSchema(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "gantry.db"))
	require.NoError(t, err)
	defer db.Close()

	for _, table := range []string{"series", "samples_1m", "samples_10m", "samples_1h", "events", "settings", "schema_migrations"} {
		var n int
		err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		require.NoError(t, err)
		require.Equal(t, 1, n, "missing table %s", table)
	}

	var mode string
	require.NoError(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode)
}

func TestOpenDBIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gantry.db")
	db, err := OpenDB(path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db2, err := OpenDB(path) // second open must not fail re-applying migrations
	require.NoError(t, err)
	defer db2.Close()

	var v int
	require.NoError(t, db2.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 1, v)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go get modernc.org/sqlite@latest github.com/stretchr/testify@latest
go test ./internal/store/ -run TestOpenDB -v
```
Expected: FAIL — `OpenDB` undefined.

- [ ] **Step 3: Write the migration SQL**

`internal/store/migrations/001_core.sql`:

```sql
CREATE TABLE series (
    id         INTEGER PRIMARY KEY,
    kind       TEXT NOT NULL,
    entity     TEXT NOT NULL,
    metric     TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (kind, entity, metric)
);

CREATE TABLE samples_1m (
    series_id INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    avg       REAL NOT NULL,
    max       REAL NOT NULL,
    PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE samples_10m (
    series_id INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    avg       REAL NOT NULL,
    max       REAL NOT NULL,
    PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE samples_1h (
    series_id INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    avg       REAL NOT NULL,
    max       REAL NOT NULL,
    PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE events (
    id       INTEGER PRIMARY KEY,
    ts       INTEGER NOT NULL,
    kind     TEXT NOT NULL,
    entity   TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info',
    detail   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_events_ts ON events (ts);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
```

- [ ] **Step 4: Write the runner**

`internal/store/migrate.go`:

```go
// Package store owns Gantry's metric storage: the live ring buffer,
// SQLite rollup tiers, events, and settings.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// OpenDB opens (creating if needed) the Gantry SQLite database at path,
// sets connection pragmas, and applies any unapplied embedded migrations.
func OpenDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; a dedicated read pool arrives with the query API phase

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	if err := applyMigrations(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func applyMigrations(db *sql.DB) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for i, name := range names {
		version := i + 1
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestOpenDB -v` → PASS. Then `go mod tidy`.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(store): sqlite open with pragmas + embedded migration runner and core schema"
```

---

### Task 3: Store — types + ring buffer

**Files:**
- Create: `internal/store/types.go`, `internal/store/ring.go`
- Test: `internal/store/ring_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (used by every later task):

```go
type SeriesKey struct{ Kind, Entity, Metric string } // kind ∈ host|container|disk|gpu|unraid
type Sample struct { TS int64; Val float64 }          // TS unix seconds
type MetricSink interface{ Record(key SeriesKey, ts int64, val float64) }

func NewRing(capacity int) *Ring
func (r *Ring) Append(s Sample)
func (r *Ring) Since(ts int64) []Sample   // samples with TS >= ts, oldest first
func (r *Ring) Latest() (Sample, bool)
func (r *Ring) Len() int
```

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingAppendAndSince(t *testing.T) {
	r := NewRing(4)
	for i := int64(1); i <= 3; i++ {
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}
	require.Equal(t, 3, r.Len())
	got := r.Since(20)
	require.Equal(t, []Sample{{TS: 20, Val: 2}, {TS: 30, Val: 3}}, got)
}

func TestRingWraparoundEvictsOldest(t *testing.T) {
	r := NewRing(3)
	for i := int64(1); i <= 5; i++ { // capacity 3, appending 5 → keeps ts 30,40,50
		r.Append(Sample{TS: i * 10, Val: float64(i)})
	}
	require.Equal(t, 3, r.Len())
	require.Equal(t, []Sample{{TS: 30, Val: 3}, {TS: 40, Val: 4}, {TS: 50, Val: 5}}, r.Since(0))

	latest, ok := r.Latest()
	require.True(t, ok)
	require.Equal(t, Sample{TS: 50, Val: 5}, latest)
}

func TestRingEmpty(t *testing.T) {
	r := NewRing(3)
	_, ok := r.Latest()
	require.False(t, ok)
	require.Empty(t, r.Since(0))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRing -v` → FAIL, `NewRing` undefined.

- [ ] **Step 3: Implement**

`internal/store/types.go`:

```go
package store

// SeriesKey identifies one metric series. Kind is the entity class
// (host, container, disk, gpu, unraid), Entity the stable identity
// (container name, disk id, "" for host), Metric the series name
// (e.g. "cpu.total", "mem.used", "net.eth0.rx").
type SeriesKey struct {
	Kind   string
	Entity string
	Metric string
}

// Sample is one measured value. TS is unix seconds.
type Sample struct {
	TS  int64
	Val float64
}

// MetricSink is what collectors (and the fake generator) write into.
type MetricSink interface {
	Record(key SeriesKey, ts int64, val float64)
}
```

`internal/store/ring.go`:

```go
package store

// Ring is a fixed-capacity FIFO of samples. Not goroutine-safe;
// callers synchronize (Live wraps rings in a lock).
type Ring struct {
	buf  []Sample
	head int // index of oldest
	n    int
}

func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]Sample, capacity)}
}

func (r *Ring) Append(s Sample) {
	if r.n < len(r.buf) {
		r.buf[(r.head+r.n)%len(r.buf)] = s
		r.n++
		return
	}
	r.buf[r.head] = s
	r.head = (r.head + 1) % len(r.buf)
}

func (r *Ring) Len() int { return r.n }

func (r *Ring) Latest() (Sample, bool) {
	if r.n == 0 {
		return Sample{}, false
	}
	return r.buf[(r.head+r.n-1)%len(r.buf)], true
}

// Since returns samples with TS >= ts, oldest first.
func (r *Ring) Since(ts int64) []Sample {
	out := make([]Sample, 0, r.n)
	for i := 0; i < r.n; i++ {
		s := r.buf[(r.head+i)%len(r.buf)]
		if s.TS >= ts {
			out = append(out, s)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestRing -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): series types, MetricSink, fixed-capacity ring buffer"
```

---

### Task 4: Store — live buffer + Record

**Files:**
- Create: `internal/store/live.go`
- Test: `internal/store/live_test.go`

**Interfaces:**
- Consumes: `Ring`, `SeriesKey`, `Sample` (Task 3).
- Produces:

```go
func NewLive(ringCap int) *Live
func (l *Live) Record(key SeriesKey, ts int64, val float64) // creates ring lazily
func (l *Live) Since(key SeriesKey, ts int64) []Sample
func (l *Live) Latest(key SeriesKey) (Sample, bool)
func (l *Live) Keys() []SeriesKey                           // sorted, stable
func (l *Live) ForEach(fn func(key SeriesKey, ring *Ring))  // under read lock; fn must not retain ring
```
`Live` is goroutine-safe. Default ringCap in production wiring: **450** (15 min at 2s).

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveRecordAndRead(t *testing.T) {
	l := NewLive(8)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	l.Record(k, 100, 42.0)
	l.Record(k, 102, 43.0)

	latest, ok := l.Latest(k)
	require.True(t, ok)
	require.Equal(t, Sample{TS: 102, Val: 43.0}, latest)
	require.Len(t, l.Since(k, 0), 2)
	require.Equal(t, []SeriesKey{k}, l.Keys())
}

func TestLiveConcurrentRecord(t *testing.T) {
	l := NewLive(512)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := SeriesKey{Kind: "container", Entity: string(rune('a' + g)), Metric: "cpu"}
			for i := int64(0); i < 100; i++ {
				l.Record(k, i, float64(i))
			}
		}(g)
	}
	wg.Wait()
	require.Len(t, l.Keys(), 8)
	for _, k := range l.Keys() {
		require.Len(t, l.Since(k, 0), 100)
	}
}
```

Run with `-race` in Step 2 — the race detector is the real assertion in the second test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestLive -race -v` → FAIL, `NewLive` undefined.

- [ ] **Step 3: Implement**

`internal/store/live.go`:

```go
package store

import (
	"sort"
	"sync"
)

// Live holds the in-RAM ring buffers for every known series.
type Live struct {
	mu      sync.RWMutex
	rings   map[SeriesKey]*Ring
	ringCap int
}

func NewLive(ringCap int) *Live {
	return &Live{rings: make(map[SeriesKey]*Ring), ringCap: ringCap}
}

func (l *Live) Record(key SeriesKey, ts int64, val float64) {
	l.mu.Lock()
	r, ok := l.rings[key]
	if !ok {
		r = NewRing(l.ringCap)
		l.rings[key] = r
	}
	r.Append(Sample{TS: ts, Val: val})
	l.mu.Unlock()
}

func (l *Live) Since(key SeriesKey, ts int64) []Sample {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if r, ok := l.rings[key]; ok {
		return r.Since(ts)
	}
	return nil
}

func (l *Live) Latest(key SeriesKey) (Sample, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if r, ok := l.rings[key]; ok {
		return r.Latest()
	}
	return Sample{}, false
}

func (l *Live) Keys() []SeriesKey {
	l.mu.RLock()
	keys := make([]SeriesKey, 0, len(l.rings))
	for k := range l.rings {
		keys = append(keys, k)
	}
	l.mu.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		return a.Metric < b.Metric
	})
	return keys
}

// ForEach runs fn for every series under the read lock.
// fn must not retain the ring or call back into Live.
func (l *Live) ForEach(fn func(key SeriesKey, ring *Ring)) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for k, r := range l.rings {
		fn(k, r)
	}
}
```

Note: `Ring` itself is unsynchronized by design; all access goes through `Live`'s lock (including `ForEach` callbacks, which run under `RLock` while writers need the write lock — no torn reads).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestLive -race -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): goroutine-safe live buffer implementing MetricSink semantics"
```

---

### Task 5: Store — Store type + series registry

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `OpenDB` (Task 2), `Live` (Task 4).
- Produces (the package's front door — later tasks hang methods off `*Store`):

```go
type Store struct{ /* db, live, series-id cache, clock */ }
func Open(path string, clock func() time.Time) (*Store, error) // clock=nil → time.Now
func (s *Store) Close() error
func (s *Store) Record(key SeriesKey, ts int64, val float64)   // satisfies MetricSink (ring only; SQLite via flusher)
func (s *Store) Live() *Live
func (s *Store) DB() *sql.DB                                    // for handlers/tests; writes stay inside store
func (s *Store) seriesID(key SeriesKey) (int64, error)          // unexported, cached, INSERT-on-miss
```

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T, clock func() time.Time) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gantry.db"), clock)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreRecordGoesToLive(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	s.Record(k, 100, 7.5)
	got, ok := s.Live().Latest(k)
	require.True(t, ok)
	require.Equal(t, 7.5, got.Val)
}

func TestSeriesIDStableAndCached(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "cpu"}

	id1, err := s.seriesID(k)
	require.NoError(t, err)
	id2, err := s.seriesID(k)
	require.NoError(t, err)
	require.Equal(t, id1, id2)

	other, err := s.seriesID(SeriesKey{Kind: "container", Entity: "jellyfin", Metric: "mem"})
	require.NoError(t, err)
	require.NotEqual(t, id1, other)

	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM series`).Scan(&n))
	require.Equal(t, 2, n)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestStore|TestSeriesID' -v` → FAIL, `Open` undefined.

- [ ] **Step 3: Implement**

`internal/store/store.go`:

```go
package store

import (
	"database/sql"
	"sync"
	"time"
)

const DefaultRingCap = 450 // 15 minutes at one sample per 2s

// Store is the front door to all Gantry persistence.
type Store struct {
	db    *sql.DB
	live  *Live
	clock func() time.Time

	idMu sync.Mutex
	ids  map[SeriesKey]int64
}

func Open(path string, clock func() time.Time) (*Store, error) {
	if clock == nil {
		clock = time.Now
	}
	db, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	return &Store{
		db:    db,
		live:  NewLive(DefaultRingCap),
		clock: clock,
		ids:   make(map[SeriesKey]int64),
	}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Live() *Live  { return s.live }
func (s *Store) DB() *sql.DB  { return s.db }

// Record satisfies MetricSink. Hot path: RAM only — SQLite is fed by
// the minute flusher, never per-tick.
func (s *Store) Record(key SeriesKey, ts int64, val float64) {
	s.live.Record(key, ts, val)
}

func (s *Store) seriesID(key SeriesKey) (int64, error) {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	if id, ok := s.ids[key]; ok {
		return id, nil
	}
	_, err := s.db.Exec(`INSERT INTO series (kind, entity, metric, created_at) VALUES (?,?,?,?)
		ON CONFLICT (kind, entity, metric) DO NOTHING`,
		key.Kind, key.Entity, key.Metric, s.clock().Unix())
	if err != nil {
		return 0, err
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM series WHERE kind=? AND entity=? AND metric=?`,
		key.Kind, key.Entity, key.Metric).Scan(&id); err != nil {
		return 0, err
	}
	s.ids[key] = id
	return id, nil
}

var _ MetricSink = (*Store)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v` → all PASS (including earlier tasks' tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): Store front door with cached series registry; Record hits RAM only"
```

---

### Task 6: Store — minute flusher (ring → samples_1m)

**Files:**
- Create: `internal/store/flush.go`
- Test: `internal/store/flush_test.go`

**Interfaces:**
- Consumes: `Store`, `Live.ForEach`, `Ring.Since`, `seriesID`.
- Produces:

```go
// FlushMinutes aggregates every complete minute since the last call
// (capped at 15 windows of catch-up) into samples_1m. First call only
// establishes the baseline. Returns number of (series, minute) rows written.
func (s *Store) FlushMinutes(now time.Time) (int, error)
```
Wiring loop (Task 12) calls this once per minute. Minute windows are `[m, m+60)`, row `ts = m`.

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func at(s string) time.Time { // "15:04:05" on a fixed day, UTC
	tm, err := time.Parse("2006-01-02 15:04:05", "2026-08-25 "+s)
	if err != nil {
		panic(err)
	}
	return tm.UTC()
}

func TestFlushMinutesAggregatesAvgAndMax(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}

	base := at("12:04:00")
	// Baseline call: establishes lastFlushed, writes nothing.
	n, err := s.FlushMinutes(base)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	// Samples inside [12:04:00, 12:05:00): values 10, 20, 60.
	s.Record(k, base.Unix()+2, 10)
	s.Record(k, base.Unix()+30, 20)
	s.Record(k, base.Unix()+58, 60)

	n, err = s.FlushMinutes(at("12:05:07")) // 12:04 window is now complete
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var avg, max float64
	var ts int64
	require.NoError(t, s.DB().QueryRow(
		`SELECT ts, avg, max FROM samples_1m LIMIT 1`).Scan(&ts, &avg, &max))
	require.Equal(t, base.Unix(), ts)
	require.InDelta(t, 30.0, avg, 0.001)
	require.Equal(t, 60.0, max)
}

func TestFlushMinutesCatchesUpMultipleWindows(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "mem.used"}
	base := at("12:00:00")
	_, err := s.FlushMinutes(base)
	require.NoError(t, err)

	for m := int64(0); m < 3; m++ { // one sample in each of 12:00, 12:01, 12:02
		s.Record(k, base.Unix()+m*60+5, float64(m))
	}
	n, err := s.FlushMinutes(at("12:03:30")) // three complete windows at once
	require.NoError(t, err)
	require.Equal(t, 3, n)

	// Idempotent: nothing new without new samples/minutes.
	n, err = s.FlushMinutes(at("12:03:45"))
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestFlushSkipsEmptyWindows(t *testing.T) {
	s := newTestStore(t, nil)
	_, err := s.FlushMinutes(at("12:00:00"))
	require.NoError(t, err)
	n, err := s.FlushMinutes(at("12:02:00")) // no samples at all
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestFlush -v` → FAIL, `FlushMinutes` undefined.

- [ ] **Step 3: Implement**

`internal/store/flush.go`:

```go
package store

import "time"

const flushCatchUpMax = 15 // ring holds 15 minutes; older windows are gone anyway

// FlushMinutes writes 1-minute (avg, max) aggregates for every complete
// minute since the previous call. The first call only records a baseline.
func (s *Store) FlushMinutes(now time.Time) (int, error) {
	nowMin := now.Unix() - now.Unix()%60

	if s.lastFlushed == 0 {
		s.lastFlushed = nowMin
		return 0, nil
	}

	written := 0
	start := s.lastFlushed
	if nowMin-start > flushCatchUpMax*60 {
		start = nowMin - flushCatchUpMax*60
	}

	for m := start; m < nowMin; m += 60 {
		type agg struct {
			key           SeriesKey
			sum, max      float64
			count         int
		}
		var aggs []agg
		s.live.ForEach(func(key SeriesKey, ring *Ring) {
			a := agg{key: key}
			for _, smp := range ring.Since(m) {
				if smp.TS >= m+60 {
					continue
				}
				a.sum += smp.Val
				if a.count == 0 || smp.Val > a.max {
					a.max = smp.Val
				}
				a.count++
			}
			if a.count > 0 {
				aggs = append(aggs, a)
			}
		})

		if len(aggs) > 0 {
			tx, err := s.db.Begin()
			if err != nil {
				return written, err
			}
			for _, a := range aggs {
				id, err := s.seriesID(a.key)
				if err != nil {
					tx.Rollback()
					return written, err
				}
				if _, err := tx.Exec(`INSERT OR REPLACE INTO samples_1m (series_id, ts, avg, max)
					VALUES (?,?,?,?)`, id, m, a.sum/float64(a.count), a.max); err != nil {
					tx.Rollback()
					return written, err
				}
				written++
			}
			if err := tx.Commit(); err != nil {
				return written, err
			}
		}
		s.lastFlushed = m + 60
	}
	if s.lastFlushed < nowMin {
		s.lastFlushed = nowMin
	}
	return written, nil
}
```

Add the field to `Store` in `internal/store/store.go` (modify the struct):

```go
type Store struct {
	db    *sql.DB
	live  *Live
	clock func() time.Time

	idMu sync.Mutex
	ids  map[SeriesKey]int64

	lastFlushed int64 // unix seconds of the last fully-flushed minute boundary
}
```

(`FlushMinutes` is called from a single goroutine — the wiring loop — so `lastFlushed` needs no lock; note this in a comment.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): minute flusher aggregates live rings into samples_1m with catch-up"
```

---

### Task 7: Store — cascade downsampler + pruner

**Files:**
- Create: `internal/store/downsample.go`
- Test: `internal/store/downsample_test.go`

**Interfaces:**
- Consumes: `Store`, schema tables, settings table (raw SQL here; the typed settings API arrives in Task 9 and is not needed).
- Produces:

```go
type Retention struct {
	R1, R2, R3   time.Duration // samples_1m, _10m, _1h ages
	SizeCapBytes int64
}
func DefaultRetention() Retention // 48h, 720h, 9490h (~13mo), 512MB
func (s *Store) DownsampleOnce(now time.Time) error // 1m→10m and 10m→1h for complete windows
func (s *Store) PruneOnce(now time.Time, ret Retention) error
```
Progress watermarks are persisted in `settings` keys `ds.last_10m`, `ds.last_1h` (unix seconds) so cascades are restart-safe.

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seed1m inserts one samples_1m row per minute over [from, to) for a fresh series.
func seed1m(t *testing.T, s *Store, key SeriesKey, from, to time.Time, val float64) {
	t.Helper()
	id, err := s.seriesID(key)
	require.NoError(t, err)
	for m := from.Unix(); m < to.Unix(); m += 60 {
		_, err := s.DB().Exec(`INSERT OR REPLACE INTO samples_1m (series_id, ts, avg, max) VALUES (?,?,?,?)`,
			id, m, val, val*2)
		require.NoError(t, err)
	}
}

func TestDownsample1mTo10m(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	seed1m(t, s, k, at("12:00:00"), at("12:20:00"), 10) // 20 minutes of avg=10, max=20

	require.NoError(t, s.DownsampleOnce(at("12:21:00")))

	rows, err := s.DB().Query(`SELECT ts, avg, max FROM samples_10m ORDER BY ts`)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var ts int64
		var avg, max float64
		require.NoError(t, rows.Scan(&ts, &avg, &max))
		got = append(got, fmt.Sprintf("%d avg=%.0f max=%.0f", ts, avg, max))
	}
	// Two complete 10m windows: 12:00 and 12:10.
	require.Equal(t, []string{
		fmt.Sprintf("%d avg=10 max=20", at("12:00:00").Unix()),
		fmt.Sprintf("%d avg=10 max=20", at("12:10:00").Unix()),
	}, got)

	// Idempotent: watermark advanced, re-run adds nothing.
	require.NoError(t, s.DownsampleOnce(at("12:21:30")))
	var n int
	require.NoError(t, s.DB().QueryRow(`SELECT count(*) FROM samples_10m`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestPruneEnforcesAges(t *testing.T) {
	s := newTestStore(t, nil)
	k := SeriesKey{Kind: "host", Metric: "cpu.total"}
	now := at("12:00:00")
	seed1m(t, s, k, now.Add(-50*time.Hour), now.Add(-49*time.Hour), 5) // older than R1=48h
	seed1m(t, s, k, now.Add(-1*time.Hour), now, 5)                     // fresh

	require.NoError(t, s.PruneOnce(now, DefaultRetention()))

	var minTS int64
	require.NoError(t, s.DB().QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&minTS))
	require.GreaterOrEqual(t, minTS, now.Add(-48*time.Hour).Unix())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestDownsample|TestPrune' -v` → FAIL, undefined functions.

- [ ] **Step 3: Implement**

`internal/store/downsample.go`:

```go
package store

import (
	"database/sql"
	"time"
)

type Retention struct {
	R1, R2, R3   time.Duration
	SizeCapBytes int64
}

func DefaultRetention() Retention {
	return Retention{
		R1:           48 * time.Hour,
		R2:           30 * 24 * time.Hour,
		R3:           13 * 30 * 24 * time.Hour,
		SizeCapBytes: 512 << 20,
	}
}

// DownsampleOnce cascades complete windows: samples_1m → samples_10m,
// then samples_10m → samples_1h. Watermarks persist in settings.
func (s *Store) DownsampleOnce(now time.Time) error {
	if err := s.cascade(now, "samples_1m", "samples_10m", 600, "ds.last_10m"); err != nil {
		return err
	}
	return s.cascade(now, "samples_10m", "samples_1h", 3600, "ds.last_1h")
}

func (s *Store) cascade(now time.Time, from, to string, window int64, watermarkKey string) error {
	upTo := (now.Unix() / window) * window // start of the current (incomplete) window

	var last int64
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, watermarkKey).Scan(&last)
	if err == sql.ErrNoRows {
		last = 0
	} else if err != nil {
		return err
	}
	if last >= upTo {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// avg-of-avgs is exact here: source windows are uniform width.
	if _, err := tx.Exec(`INSERT OR REPLACE INTO `+to+` (series_id, ts, avg, max)
		SELECT series_id, (ts/?)*?, AVG(avg), MAX(max) FROM `+from+`
		WHERE ts >= ? AND ts < ?
		GROUP BY series_id, ts/?`,
		window, window, last, upTo, window); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?,?,?)`,
		watermarkKey, upTo, now.Unix()); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// PruneOnce deletes rows past each tier's retention, prunes events past R3,
// and enforces the DB size cap by trimming the oldest samples_1m first.
func (s *Store) PruneOnce(now time.Time, ret Retention) error {
	cut := func(table string, age time.Duration) error {
		_, err := s.db.Exec(`DELETE FROM `+table+` WHERE ts < ?`, now.Add(-age).Unix())
		return err
	}
	if err := cut("samples_1m", ret.R1); err != nil {
		return err
	}
	if err := cut("samples_10m", ret.R2); err != nil {
		return err
	}
	if err := cut("samples_1h", ret.R3); err != nil {
		return err
	}
	if err := cut("events", ret.R3); err != nil {
		return err
	}

	// Size cap: trim oldest R1 data in 6h bites until under cap (R1 is
	// always the bulk; give up after 8 bites rather than loop forever).
	for i := 0; i < 8; i++ {
		var pages, pageSize int64
		if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
			return err
		}
		if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
			return err
		}
		if pages*pageSize <= ret.SizeCapBytes {
			break
		}
		var oldest sql.NullInt64
		if err := s.db.QueryRow(`SELECT min(ts) FROM samples_1m`).Scan(&oldest); err != nil {
			return err
		}
		if !oldest.Valid {
			break
		}
		if _, err := s.db.Exec(`DELETE FROM samples_1m WHERE ts < ?`, oldest.Int64+6*3600); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): restart-safe cascade downsampler and retention/size pruner"
```

---

### Task 8: Store — events

**Files:**
- Create: `internal/store/events.go`
- Test: `internal/store/events_test.go`

**Interfaces:**
- Consumes: `Store`.
- Produces (Phase 2 collectors append; Phase 3 API queries; Phase 4 alerts append):

```go
type Event struct {
	ID       int64
	TS       int64  // unix seconds
	Kind     string // e.g. container.start, container.oom, array.state, parity.finish, alert.fired
	Entity   string
	Severity string // info|warning|alert
	Detail   string
}
type EventFilter struct {
	Kinds    []string // empty = all
	Entity   string   // "" = all
	From, To int64    // 0 = unbounded
	Limit    int      // 0 → 100
}
func (s *Store) AppendEvent(e Event) (int64, error)      // returns id; TS=0 → clock()
func (s *Store) QueryEvents(f EventFilter) ([]Event, error) // newest first
```

- [ ] **Step 1: Write the failing test** (note the injected clock — `newTestStore` takes `func() time.Time`)

```go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAppendAndQueryEvents(t *testing.T) {
	s := newTestStore(t, func() time.Time { return at("12:00:00") })

	_, err := s.AppendEvent(Event{Kind: "container.start", Entity: "jellyfin"})
	require.NoError(t, err)
	_, err = s.AppendEvent(Event{TS: at("12:01:00").Unix(), Kind: "container.oom", Entity: "jellyfin", Severity: "alert", Detail: "oom-killed"})
	require.NoError(t, err)
	_, err = s.AppendEvent(Event{TS: at("12:02:00").Unix(), Kind: "array.state", Entity: "array", Detail: "STARTED"})
	require.NoError(t, err)

	all, err := s.QueryEvents(EventFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, "array.state", all[0].Kind) // newest first

	jelly, err := s.QueryEvents(EventFilter{Entity: "jellyfin"})
	require.NoError(t, err)
	require.Len(t, jelly, 2)

	ooms, err := s.QueryEvents(EventFilter{Kinds: []string{"container.oom"}})
	require.NoError(t, err)
	require.Len(t, ooms, 1)
	require.Equal(t, "alert", ooms[0].Severity)

	windowed, err := s.QueryEvents(EventFilter{From: at("12:00:30").Unix(), To: at("12:01:30").Unix()})
	require.NoError(t, err)
	require.Len(t, windowed, 1)
	require.Equal(t, "container.oom", windowed[0].Kind)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestAppendAndQueryEvents -v` → FAIL.

- [ ] **Step 3: Implement**

`internal/store/events.go`:

```go
package store

import "strings"

type Event struct {
	ID       int64
	TS       int64
	Kind     string
	Entity   string
	Severity string
	Detail   string
}

type EventFilter struct {
	Kinds    []string
	Entity   string
	From, To int64
	Limit    int
}

func (s *Store) AppendEvent(e Event) (int64, error) {
	if e.TS == 0 {
		e.TS = s.clock().Unix()
	}
	if e.Severity == "" {
		e.Severity = "info"
	}
	res, err := s.db.Exec(`INSERT INTO events (ts, kind, entity, severity, detail) VALUES (?,?,?,?,?)`,
		e.TS, e.Kind, e.Entity, e.Severity, e.Detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) QueryEvents(f EventFilter) ([]Event, error) {
	q := `SELECT id, ts, kind, entity, severity, detail FROM events WHERE 1=1`
	var args []any
	if len(f.Kinds) > 0 {
		q += ` AND kind IN (?` + strings.Repeat(",?", len(f.Kinds)-1) + `)`
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.Entity != "" {
		q += ` AND entity = ?`
		args = append(args, f.Entity)
	}
	if f.From > 0 {
		q += ` AND ts >= ?`
		args = append(args, f.From)
	}
	if f.To > 0 {
		q += ` AND ts <= ?`
		args = append(args, f.To)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Entity, &e.Severity, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): events append/query with kind, entity, and window filters"
```

---

### Task 9: Store settings + config resolver

**Files:**
- Create: `internal/store/settings.go`, `internal/config/config.go`
- Test: `internal/store/settings_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `Store`.
- Produces:

```go
// store
func (s *Store) SettingGet(key string) (string, bool, error)
func (s *Store) SettingSet(key, value string) error

// config — precedence: env GANTRY_<KEY with dots→underscores, upper> > settings row > default
type Config struct{ /* store + getenv */ }
func New(st *store.Store, getenv func(string) string) *Config
func (c *Config) String(key, def string) string
func (c *Config) Int(key string, def int) int
func (c *Config) Bool(key string, def bool) bool // truthy: 1,true,yes,on (case-insensitive)
```
Canonical keys this phase: `port` (default 8380), `fake_data` (default false).

- [ ] **Step 1: Write the failing tests**

`internal/store/settings_test.go`:

```go
package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t, nil)

	_, ok, err := s.SettingGet("port")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.SettingSet("port", "9000"))
	v, ok, err := s.SettingGet("port")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "9000", v)

	require.NoError(t, s.SettingSet("port", "9001")) // upsert
	v, _, _ = s.SettingGet("port")
	require.Equal(t, "9001", v)
}
```

`internal/config/config_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func testCfg(t *testing.T, env map[string]string) (*Config, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return New(st, func(k string) string { return env[k] }), st
}

func TestPrecedenceEnvOverSettingOverDefault(t *testing.T) {
	c, st := testCfg(t, map[string]string{"GANTRY_PORT": "7777"})
	require.NoError(t, st.SettingSet("port", "9000"))
	require.Equal(t, 7777, c.Int("port", 8380)) // env wins

	c2, st2 := testCfg(t, nil)
	require.NoError(t, st2.SettingSet("port", "9000"))
	require.Equal(t, 9000, c2.Int("port", 8380)) // setting beats default

	c3, _ := testCfg(t, nil)
	require.Equal(t, 8380, c3.Int("port", 8380)) // default
}

func TestBoolAndKeyMapping(t *testing.T) {
	c, _ := testCfg(t, map[string]string{"GANTRY_FAKE_DATA": "true"})
	require.True(t, c.Bool("fake_data", false))

	c2, _ := testCfg(t, map[string]string{"GANTRY_RETENTION_R1_HOURS": "24"})
	require.Equal(t, 24, c2.Int("retention.r1_hours", 48)) // dots → underscores
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestSettings -v && go test ./internal/config/ -v` → FAIL, undefined.

- [ ] **Step 3: Implement**

`internal/store/settings.go`:

```go
package store

import "database/sql"

func (s *Store) SettingGet(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) SettingSet(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?,?,?)`,
		key, value, s.clock().Unix())
	return err
}
```

`internal/config/config.go`:

```go
// Package config resolves runtime configuration with the precedence
// env (GANTRY_*) > settings table > compiled default.
package config

import (
	"strconv"
	"strings"

	"github.com/smidley/gantry/internal/store"
)

type Config struct {
	st     *store.Store
	getenv func(string) string
}

func New(st *store.Store, getenv func(string) string) *Config {
	return &Config{st: st, getenv: getenv}
}

func envName(key string) string {
	return "GANTRY_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func (c *Config) String(key, def string) string {
	if v := c.getenv(envName(key)); v != "" {
		return v
	}
	if v, ok, err := c.st.SettingGet(key); err == nil && ok {
		return v
	}
	return def
}

func (c *Config) Int(key string, def int) int {
	if n, err := strconv.Atoi(c.String(key, strconv.Itoa(def))); err == nil {
		return n
	}
	return def
}

func (c *Config) Bool(key string, def bool) bool {
	v := strings.ToLower(c.String(key, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -race` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(config): settings table accessors and env>setting>default resolver"
```

---

### Task 10: Fake-data generator

**Files:**
- Create: `internal/fake/fake.go`
- Test: `internal/fake/fake_test.go`

**Interfaces:**
- Consumes: `store.MetricSink`, `store.SeriesKey` (Task 3).
- Produces:

```go
// New creates a deterministic generator (seeded) simulating one host
// and len(archetypes) containers through the same MetricSink real
// collectors will use.
func New(sink store.MetricSink, seed int64) *Generator
func (g *Generator) Tick(now time.Time) // one 2s sample for every series
func (g *Generator) Run(ctx context.Context, interval time.Duration, clock func() time.Time)
```
Series it emits (Phase 3 UI dev renders exactly these): host `cpu.total`, `mem.used_pct`, `net.rx_bps`, `net.tx_bps`, `diskio.read_bps`, `diskio.write_bps`; per container `cpu.pct`, `mem.bytes`, `net.rx_bps`, `net.tx_bps`.

- [ ] **Step 1: Write the failing test**

```go
package fake

import (
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

type capture struct{ recs map[store.SeriesKey][]store.Sample }

func (c *capture) Record(k store.SeriesKey, ts int64, v float64) {
	if c.recs == nil {
		c.recs = map[store.SeriesKey][]store.Sample{}
	}
	c.recs[k] = append(c.recs[k], store.Sample{TS: ts, Val: v})
}

func TestTickEmitsHostAndContainerSeries(t *testing.T) {
	sink := &capture{}
	g := New(sink, 1)
	now := time.Unix(1_000_000, 0)
	g.Tick(now)
	g.Tick(now.Add(2 * time.Second))

	require.Len(t, sink.recs[store.SeriesKey{Kind: "host", Metric: "cpu.total"}], 2)

	containers := map[string]bool{}
	for k := range sink.recs {
		if k.Kind == "container" {
			containers[k.Entity] = true
		}
	}
	require.GreaterOrEqual(t, len(containers), 15, "want a fleet worth rendering")

	for k, samples := range sink.recs {
		for _, s := range samples {
			if k.Metric == "cpu.total" || k.Metric == "cpu.pct" || k.Metric == "mem.used_pct" {
				require.GreaterOrEqual(t, s.Val, 0.0, "%v", k)
				require.LessOrEqual(t, s.Val, 100.0, "%v", k)
			}
		}
	}
}

func TestDeterministicWithSameSeed(t *testing.T) {
	a, b := &capture{}, &capture{}
	now := time.Unix(1_000_000, 0)
	New(a, 42).Tick(now)
	New(b, 42).Tick(now)
	require.Equal(t, a.recs, b.recs)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fake/ -v` → FAIL, undefined.

- [ ] **Step 3: Implement**

`internal/fake/fake.go`:

```go
// Package fake synthesizes a plausible Unraid box (host + container fleet)
// through the production MetricSink path, for UI development and demos.
// Enabled by GANTRY_FAKE_DATA=1. Never active by default.
package fake

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/smidley/gantry/internal/store"
)

type archetype struct {
	name     string
	cpuBase  float64 // steady CPU %
	cpuAmp   float64 // sinusoidal swing
	cpuSpike float64 // probability per tick of a hard spike
	memBytes float64
	netScale float64 // bytes/s magnitude
}

var fleet = []archetype{
	{"jellyfin", 4, 3, 0.02, 900e6, 4e6},
	{"plex", 3, 2, 0.02, 800e6, 3e6},
	{"radarr", 1, 1, 0.005, 300e6, 2e5},
	{"sonarr", 1, 1, 0.005, 320e6, 2e5},
	{"prowlarr", 0.5, 0.5, 0.002, 150e6, 5e4},
	{"qbittorrent", 6, 4, 0.01, 500e6, 8e6},
	{"sabnzbd", 2, 6, 0.01, 400e6, 9e6},
	{"postgres", 2, 0.5, 0.001, 1.2e9, 1e5},
	{"redis", 0.5, 0.2, 0.001, 200e6, 8e4},
	{"homeassistant", 3, 1, 0.005, 700e6, 1e5},
	{"grafana", 1, 0.5, 0.002, 250e6, 6e4},
	{"pihole", 0.5, 0.3, 0.001, 120e6, 4e4},
	{"nginx", 0.3, 0.2, 0.001, 80e6, 5e5},
	{"vaultwarden", 0.2, 0.1, 0.001, 90e6, 1e4},
	{"immich", 5, 4, 0.02, 1.5e9, 1e6},
	{"paperless", 1, 2, 0.01, 400e6, 8e4},
	{"gitea", 0.5, 0.5, 0.002, 300e6, 6e4},
	{"minecraft", 8, 5, 0.01, 2.5e9, 3e5},
	{"frigate", 12, 4, 0.02, 1.1e9, 5e6},
	{"unifi-controller", 2, 1, 0.005, 900e6, 2e5},
}

type Generator struct {
	sink store.MetricSink
	rng  *rand.Rand
}

func New(sink store.MetricSink, seed int64) *Generator {
	return &Generator{sink: sink, rng: rand.New(rand.NewSource(seed))}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Tick emits one sample per series for the instant `now`.
func (g *Generator) Tick(now time.Time) {
	ts := now.Unix()
	phase := float64(ts) / 300.0 // slow 5-minute swells

	hostCPU := 0.0
	for i, a := range fleet {
		cpu := a.cpuBase + a.cpuAmp*math.Sin(phase+float64(i)) + g.rng.Float64()
		if g.rng.Float64() < a.cpuSpike {
			cpu += 30 + g.rng.Float64()*40
		}
		cpu = clamp(cpu, 0, 100)
		hostCPU += cpu

		mem := a.memBytes * (0.9 + 0.2*g.rng.Float64())
		rx := a.netScale * (0.5 + g.rng.Float64())
		tx := a.netScale * 0.2 * (0.5 + g.rng.Float64())

		e := a.name
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "cpu.pct"}, ts, cpu)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "mem.bytes"}, ts, mem)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "net.rx_bps"}, ts, rx)
		g.sink.Record(store.SeriesKey{Kind: "container", Entity: e, Metric: "net.tx_bps"}, ts, tx)
	}

	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "cpu.total"}, ts, clamp(hostCPU/3+5, 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "mem.used_pct"}, ts, clamp(55+10*math.Sin(phase/3)+2*g.rng.Float64(), 0, 100))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "net.rx_bps"}, ts, 20e6*(0.5+g.rng.Float64()))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "net.tx_bps"}, ts, 5e6*(0.5+g.rng.Float64()))
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio.read_bps"}, ts, 30e6*g.rng.Float64())
	g.sink.Record(store.SeriesKey{Kind: "host", Metric: "diskio.write_bps"}, ts, 15e6*g.rng.Float64())
}

// Run ticks until ctx is done. clock defaults to time.Now when nil.
func (g *Generator) Run(ctx context.Context, interval time.Duration, clock func() time.Time) {
	if clock == nil {
		clock = time.Now
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Tick(clock())
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fake/ -race -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(fake): deterministic synthetic fleet through the production MetricSink path"
```

---

### Task 11: Server — healthz, version, placeholder page

**Files:**
- Create: `internal/server/server.go`, `internal/server/webdist/index.html`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `store.Store` (health will later report source status; this phase reports `sources: {}`).
- Produces:

```go
type Options struct {
	Port    int
	Version string
	Store   *store.Store
	Started time.Time
}
func New(o Options) *Server
func (s *Server) Handler() http.Handler          // for tests and healthcheck reuse
func (s *Server) ListenAndServe(ctx context.Context) error // graceful shutdown on ctx.Done
```
Routes: `GET /api/healthz` → `{"status":"ok","version":"...","uptime_s":N,"sources":{}}`; `GET /api/version` → `{"version":"..."}`; `GET /` → embedded placeholder.

- [ ] **Step 1: Write the failing test**

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthz(t *testing.T) {
	s := New(Options{Port: 0, Version: "test-1", Started: time.Now().Add(-90 * time.Second)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body struct {
		Status  string          `json:"status"`
		Version string          `json:"version"`
		UptimeS int64           `json:"uptime_s"`
		Sources map[string]bool `json:"sources"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body.Status)
	require.Equal(t, "test-1", body.Version)
	require.GreaterOrEqual(t, body.UptimeS, int64(90))
	require.NotNil(t, body.Sources)
}

func TestRootServesPlaceholder(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -v` → FAIL, undefined.

- [ ] **Step 3: Implement**

`internal/server/webdist/index.html`:

```html
<!doctype html>
<meta charset="utf-8">
<title>Gantry</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; display: grid; place-items: center;
         min-height: 100vh; margin: 0; background: #0f172a; color: #e2e8f0; }
  @media (prefers-color-scheme: light) { body { background: #f8fafc; color: #0f172a; } }
</style>
<div>
  <h1>Gantry</h1>
  <p>The UI ships in Phase 3. <a href="/api/healthz">healthz</a></p>
</div>
```

`internal/server/server.go`:

```go
// Package server hosts Gantry's HTTP surface: the embedded SPA and /api.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/smidley/gantry/internal/store"
)

//go:embed webdist
var webFS embed.FS

type Options struct {
	Port    int
	Version string
	Store   *store.Store
	Started time.Time
}

type Server struct {
	opts Options
	mux  *http.ServeMux
}

func New(o Options) *Server {
	s := &Server{opts: o, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /api/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"version": s.opts.Version})
	})

	dist, err := fs.Sub(webFS, "webdist")
	if err != nil {
		panic(err) // embedded FS shape is a compile-time property
	}
	s.mux.Handle("GET /", http.FileServerFS(dist))
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"status":   "ok",
		"version":  s.opts.Version,
		"uptime_s": int64(time.Since(s.opts.Started).Seconds()),
		"sources":  map[string]bool{}, // Phase 2 fills this from the source registry
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ListenAndServe serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	hs := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.opts.Port),
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- hs.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(server): http skeleton with healthz/version and embedded placeholder page"
```

---

### Task 12: Main wiring + `-healthcheck` mode

**Files:**
- Create: `cmd/gantry/main.go`
- Test: `cmd/gantry/main_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: the `gantry` binary. Behavior:
  - default run: open store at `$GANTRY_DB_PATH` or `/config/gantry.db` → start fake generator if `fake_data` → start maintenance loop (flush every 60s; downsample+prune every 10min) → serve HTTP on `port`.
  - `gantry -healthcheck`: GET `http://127.0.0.1:<port>/api/healthz`, exit 0 on HTTP 200, else exit 1 (used by Docker `HEALTHCHECK`).

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

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
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not shut down")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/gantry/ -v` → FAIL, `run`/`healthcheck` undefined.

- [ ] **Step 3: Implement**

`cmd/gantry/main.go`:

```go
// Command gantry is the Gantry monitor: collectors, storage, and web UI
// in one binary. See docs/superpowers/specs/ for the design.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/smidley/gantry/internal/config"
	"github.com/smidley/gantry/internal/fake"
	"github.com/smidley/gantry/internal/server"
	"github.com/smidley/gantry/internal/store"
)

var version = "dev" // set via -ldflags at build

func main() {
	hc := flag.Bool("healthcheck", false, "probe the local healthz endpoint and exit")
	flag.Parse()

	if *hc {
		if err := healthcheck(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, version); err != nil {
		log.Fatal(err)
	}
}

// envOnly resolves keys that must work before the store exists.
func envOnly(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, getenv func(string) string, ver string) error {
	dbPath := envOnly(getenv, "GANTRY_DB_PATH", "/config/gantry.db")

	st, err := store.Open(dbPath, nil)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	cfg := config.New(st, getenv)
	port := cfg.Int("port", 8380)

	if cfg.Bool("fake_data", false) {
		log.Println("fake data mode: synthesizing a demo fleet")
		go fake.New(st, time.Now().UnixNano()).Run(ctx, 2*time.Second, nil)
	}

	// Maintenance: flush every minute; downsample + prune every 10 minutes.
	go func() {
		flush := time.NewTicker(60 * time.Second)
		deep := time.NewTicker(10 * time.Minute)
		defer flush.Stop()
		defer deep.Stop()
		ret := store.DefaultRetention()
		for {
			select {
			case <-ctx.Done():
				return
			case <-flush.C:
				if _, err := st.FlushMinutes(time.Now()); err != nil {
					log.Println("flush:", err)
				}
			case <-deep.C:
				if err := st.DownsampleOnce(time.Now()); err != nil {
					log.Println("downsample:", err)
				}
				if err := st.PruneOnce(time.Now(), ret); err != nil {
					log.Println("prune:", err)
				}
			}
		}
	}()

	log.Printf("gantry %s listening on :%d", ver, port)
	return server.New(server.Options{
		Port:    port,
		Version: ver,
		Store:   st,
		Started: time.Now(),
	}).ListenAndServe(ctx)
}

func healthcheck(getenv func(string) string) error {
	port := envOnly(getenv, "GANTRY_PORT", "8380")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
```

(Note: `healthcheck` reads `GANTRY_PORT` from env only — inside the container that env var is either set by the user or the default 8380 matches the server's own default resolution. A settings-table port override without the env var would desync the probe; acceptable this phase, revisited when the settings UI lands.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -race` → all PASS. Then `make build` → binary builds. Run `./gantry -healthcheck; echo $?` → prints unhealthy + `1` (nothing listening — expected).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: wire main run loop, maintenance tickers, and -healthcheck probe mode"
```

---

### Task 13: Dockerfile (scratch, static, healthcheck)

**Files:**
- Create: `Dockerfile`, `.dockerignore`

**Interfaces:**
- Consumes: the `gantry` binary build.
- Produces: `gantry:dev` image — `scratch` base, `/gantry` entrypoint, `HEALTHCHECK` exec-form, `VOLUME /config`, `EXPOSE 8380`.

- [ ] **Step 1: Write the files**

`.dockerignore`:

```
.git
docs
*.md
gantry
spikeprobe
```

`Dockerfile`:

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gantry ./cmd/gantry

FROM scratch
COPY --from=build /out/gantry /gantry
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
VOLUME /config
EXPOSE 8380
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/gantry", "-healthcheck"]
ENTRYPOINT ["/gantry"]
```

(Go's `ZONEINFO=zoneinfo.zip` trick avoids copying the whole zoneinfo tree; ca-certificates are included now so Phase 4 webhooks work without image changes.)

- [ ] **Step 2: Build and verify**

Run: `make docker`

If Docker isn't available on this machine, fall back to verifying the static binary cross-builds and mark the container run for the spike-deployment task:
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/gantry`

If Docker IS available (scratch has no writable paths, so `/config` must be a mount):

```bash
docker run -d --rm --name gantry-smoke -p 18380:8380 -e GANTRY_FAKE_DATA=1 -v "$(mktemp -d)":/config gantry:dev
sleep 2
curl -sf http://localhost:18380/api/healthz
docker inspect --format '{{.State.Health.Status}}' gantry-smoke
docker stop gantry-smoke
```

Expected: healthz JSON with `"status":"ok"`; health status `starting` or `healthy`.

- [ ] **Step 3: Check image size**

Run: `docker images gantry:dev --format '{{.Size}}'`
Expected: well under 25MB (spec budget).

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "build: scratch Dockerfile with exec-form healthcheck and zoneinfo"
```

---

### Task 14: GPU fdinfo parser (production code, spike-shared)

**Files:**
- Create: `internal/collect/gpu/fdinfo.go`, `internal/collect/gpu/testdata/i915_client.txt`, `internal/collect/gpu/testdata/amdgpu_client.txt`, `internal/collect/gpu/testdata/not_drm.txt`
- Test: `internal/collect/gpu/fdinfo_test.go`

**Interfaces:**
- Consumes: nothing (pure function over file contents).
- Produces (Phase 2's GPU collector and Task 17's spike both call this):

```go
// FDInfo is the raw drm-* key/value view of one /proc/<pid>/fdinfo/<fd> file.
type FDInfo struct {
	Driver   string            // drm-driver value; "" if not a DRM client file
	ClientID string            // drm-client-id value
	Fields   map[string]string // every drm-* key verbatim (engines, cycles, memory)
}
// ParseFDInfo returns ok=false when the content has no drm-driver line.
func ParseFDInfo(r io.Reader) (FDInfo, bool)
```
Interpretation into busy-% stays in Phase 2 — deliberately, because S1's raw field dump from the real box decides whether we read `drm-engine-*` (i915 ns counters) or `drm-cycles-*` (xe).

- [ ] **Step 1: Create fixtures**

`internal/collect/gpu/testdata/i915_client.txt` (shape produced by i915 on modern kernels):

```
pos:	0
flags:	02100002
mnt_id:	26
ino:	1554
drm-driver:	i915
drm-client-id:	972
drm-pdev:	0000:00:02.0
drm-total-system0:	1024 KiB
drm-shared-system0:	0
drm-engine-render:	137463412963 ns
drm-engine-copy:	21446261 ns
drm-engine-video:	507869073 ns
drm-engine-video-enhance:	0 ns
```

`internal/collect/gpu/testdata/amdgpu_client.txt`:

```
pos:	0
flags:	02100002
mnt_id:	26
ino:	1099
drm-driver:	amdgpu
drm-client-id:	460
drm-pdev:	0000:0a:00.0
drm-memory-vram:	524288 KiB
drm-memory-gtt:	12288 KiB
drm-engine-gfx:	25123456789 ns
drm-engine-dec:	102345678 ns
drm-engine-enc:	0 ns
```

`internal/collect/gpu/testdata/not_drm.txt`:

```
pos:	0
flags:	0100002
mnt_id:	11
ino:	40
```

- [ ] **Step 2: Write the failing test**

```go
package gpu

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func parseFixture(t *testing.T, name string) (FDInfo, bool) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	require.NoError(t, err)
	defer f.Close()
	return ParseFDInfo(f)
}

func TestParseI915Client(t *testing.T) {
	info, ok := parseFixture(t, "i915_client.txt")
	require.True(t, ok)
	require.Equal(t, "i915", info.Driver)
	require.Equal(t, "972", info.ClientID)
	require.Equal(t, "137463412963 ns", info.Fields["drm-engine-render"])
	require.Equal(t, "507869073 ns", info.Fields["drm-engine-video"])
}

func TestParseAmdgpuClient(t *testing.T) {
	info, ok := parseFixture(t, "amdgpu_client.txt")
	require.True(t, ok)
	require.Equal(t, "amdgpu", info.Driver)
	require.Equal(t, "524288 KiB", info.Fields["drm-memory-vram"])
}

func TestNonDRMFileRejected(t *testing.T) {
	_, ok := parseFixture(t, "not_drm.txt")
	require.False(t, ok)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/collect/gpu/ -v` → FAIL, undefined.

- [ ] **Step 4: Implement**

`internal/collect/gpu/fdinfo.go`:

```go
// Package gpu reads per-process GPU accounting from DRM fdinfo,
// the mechanism behind Gantry's per-container Intel/AMD GPU stats.
package gpu

import (
	"bufio"
	"io"
	"strings"
)

type FDInfo struct {
	Driver   string
	ClientID string
	Fields   map[string]string
}

// ParseFDInfo scans one fdinfo file. Lines are "key:\tvalue"; only
// drm-* keys are retained. ok=false when no drm-driver line is present.
func ParseFDInfo(r io.Reader) (FDInfo, bool) {
	info := FDInfo{Fields: make(map[string]string)}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		k, v, found := strings.Cut(line, ":")
		if !found || !strings.HasPrefix(k, "drm-") {
			continue
		}
		v = strings.TrimSpace(v)
		info.Fields[k] = v
		switch k {
		case "drm-driver":
			info.Driver = v
		case "drm-client-id":
			info.ClientID = v
		}
	}
	if info.Driver == "" {
		return FDInfo{}, false
	}
	return info, true
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/collect/gpu/ -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(gpu): generic DRM fdinfo parser with i915/amdgpu fixtures"
```

---

### Task 15: cgroup → container-ID extractor (production code, spike-shared)

**Files:**
- Create: `internal/collect/cgroup/cgroup.go`, fixtures inline in test
- Test: `internal/collect/cgroup/cgroup_test.go`

**Interfaces:**
- Consumes: nothing (pure function).
- Produces (Phase 2 GPU/docker collectors + Task 17 spike):

```go
// ContainerID extracts a 64-hex docker container id from the content
// of /proc/<pid>/cgroup. ok=false for non-container processes.
func ContainerID(content string) (string, bool)
```

- [ ] **Step 1: Write the failing test**

```go
package cgroup

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerID(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{
			name:    "cgroup v2 unraid",
			content: "0::/docker/8f2f3a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7\n",
			want:    "8f2f3a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7",
			ok:      true,
		},
		{
			name: "cgroup v1 multiline",
			content: "12:pids:/docker/abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd\n" +
				"11:memory:/docker/abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd\n",
			want: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
			ok:   true,
		},
		{
			name:    "host process",
			content: "0::/init.scope\n",
			ok:      false,
		},
		{
			name:    "cgroup v2 systemd-style scope",
			content: "0::/system.slice/docker-1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef.scope\n",
			want:    "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			ok:      true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ContainerID(c.content)
			require.Equal(t, c.ok, ok)
			require.Equal(t, c.want, got)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collect/cgroup/ -v` → FAIL.

- [ ] **Step 3: Implement**

`internal/collect/cgroup/cgroup.go`:

```go
// Package cgroup maps host PIDs to docker container IDs via /proc/<pid>/cgroup.
package cgroup

import "regexp"

var idRe = regexp.MustCompile(`(?:docker[/-])([0-9a-f]{64})`)

func ContainerID(content string) (string, bool) {
	m := idRe.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return m[1], true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/collect/cgroup/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(cgroup): PID-to-container-id extraction for v1 and v2 layouts"
```

---

### Task 16: Unraid notify-file writer (production code, spike-shared)

**Files:**
- Create: `internal/alert/notify.go`
- Test: `internal/alert/notify_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (Phase 4 alert delivery + Task 17 spike S2):

```go
type Notification struct {
	Event       string // shown as the notification's event/category
	Subject     string
	Description string
	Importance  string // normal | warning | alert
	Link        string // optional, e.g. "/Docker"
}
// WriteNotify atomically writes a dynamix-format notify file into
// <dir>/unread and returns the file path. now stamps the timestamp.
func WriteNotify(dir string, n Notification, now time.Time) (string, error)
```
File format written (S2 verifies dynamix accepts exactly this; constants live here for easy pinning):

```
timestamp=<unix seconds>
event=<Event>
subject=<Subject>
description=<Description>
importance=<Importance>
link=<Link, omitted when empty>
```

- [ ] **Step 1: Write the failing test**

```go
package alert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteNotifyFormat(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_756_000_000, 0)
	path, err := WriteNotify(dir, Notification{
		Event:       "Gantry",
		Subject:     "Container jellyfin unhealthy",
		Description: "health check failing for 5m",
		Importance:  "alert",
		Link:        "/Docker",
	}, now)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(path, filepath.Join(dir, "unread")+string(os.PathSeparator)))
	require.True(t, strings.HasSuffix(path, ".notify"))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t,
		"timestamp=1756000000\n"+
			"event=Gantry\n"+
			"subject=Container jellyfin unhealthy\n"+
			"description=health check failing for 5m\n"+
			"importance=alert\n"+
			"link=/Docker\n",
		string(body))
}

func TestWriteNotifyValidatesImportanceAndStripsNewlines(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteNotify(dir, Notification{Event: "x", Subject: "s", Importance: "urgent"}, time.Now())
	require.Error(t, err)

	path, err := WriteNotify(dir, Notification{
		Event: "Gantry", Subject: "line1\nline2", Description: "d\r\nd2", Importance: "normal",
	}, time.Unix(1, 0))
	require.NoError(t, err)
	body, _ := os.ReadFile(path)
	require.NotContains(t, strings.TrimSuffix(string(body), "\n"), "\r")
	require.Contains(t, string(body), "subject=line1 line2\n")
}

func TestWriteNotifyUniqueNames(t *testing.T) {
	dir := t.TempDir()
	n := Notification{Event: "Gantry", Subject: "s", Importance: "normal"}
	p1, err := WriteNotify(dir, n, time.Unix(1, 0))
	require.NoError(t, err)
	p2, err := WriteNotify(dir, n, time.Unix(1, 0))
	require.NoError(t, err)
	require.NotEqual(t, p1, p2)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/alert/ -v` → FAIL.

- [ ] **Step 3: Implement**

`internal/alert/notify.go`:

```go
// Package alert delivers Gantry alerts. This file implements the
// Unraid-native channel: dynamix-format notify files dropped into the
// mounted /tmp/notifications spool.
package alert

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type Notification struct {
	Event       string
	Subject     string
	Description string
	Importance  string
	Link        string
}

var notifySeq atomic.Uint64

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", " ")
}

// WriteNotify writes the file atomically (temp file + rename) so the
// dynamix poller never reads a partial notification.
func WriteNotify(dir string, n Notification, now time.Time) (string, error) {
	switch n.Importance {
	case "normal", "warning", "alert":
	default:
		return "", fmt.Errorf("invalid importance %q (want normal|warning|alert)", n.Importance)
	}

	unread := filepath.Join(dir, "unread")
	if err := os.MkdirAll(unread, 0o755); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "timestamp=%d\n", now.Unix())
	fmt.Fprintf(&b, "event=%s\n", sanitize(n.Event))
	fmt.Fprintf(&b, "subject=%s\n", sanitize(n.Subject))
	fmt.Fprintf(&b, "description=%s\n", sanitize(n.Description))
	fmt.Fprintf(&b, "importance=%s\n", n.Importance)
	if n.Link != "" {
		fmt.Fprintf(&b, "link=%s\n", sanitize(n.Link))
	}

	name := fmt.Sprintf("gantry_%d_%d.notify", now.UnixNano(), notifySeq.Add(1))
	tmp := filepath.Join(unread, "."+name+".tmp")
	final := filepath.Join(unread, name)
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return final, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/alert/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(alert): atomic dynamix notify-file writer (format pinned by spike S2)"
```

---

### Task 17: Spike probe binary

**Files:**
- Create: `cmd/spikeprobe/main.go`
- Test: build-only (the probe's assertions run on the Unraid box in Task 19; its parsers are already unit-tested in Tasks 14–16)

**Interfaces:**
- Consumes: `gpu.ParseFDInfo` (Task 14), `cgroup.ContainerID` (Task 15), `alert.WriteNotify` (Task 16).
- Produces: `spikeprobe` binary with `-s1 -s2 -s3 -all` flags printing `S<n> PASS/FAIL: <detail>` lines plus raw dumps for fixture capture.

- [ ] **Step 1: Implement**

`cmd/spikeprobe/main.go`:

```go
// Command spikeprobe verifies Gantry's three day-one access assumptions
// on a real Unraid box (spec §13). Throwaway harness around production
// parsers. Run it inside a container with the Gantry template flags:
//
//	docker run --rm --pid=host --cap-add=SYS_PTRACE \
//	  -v /sys:/host/sys:ro -v /var/local/emhttp:/unraid:ro \
//	  -v /tmp/notifications:/notify \
//	  -v /var/run/docker.sock:/var/run/docker.sock:ro \
//	  <image> -all
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/collect/cgroup"
	"github.com/smidley/gantry/internal/collect/gpu"
)

func main() {
	s1 := flag.Bool("s1", false, "S1: foreign-process DRM fdinfo readability")
	s2 := flag.Bool("s2", false, "S2: write a test notification into the unraid spool")
	s3 := flag.Bool("s3", false, "S3: cgroup v2 readability under /host/sys")
	all := flag.Bool("all", false, "run all spikes")
	flag.Parse()

	fail := false
	if *all || *s1 {
		fail = !runS1() || fail
	}
	if *all || *s3 {
		fail = !runS3() || fail
	}
	if *all || *s2 {
		fail = !runS2() || fail
	}
	if fail {
		os.Exit(1)
	}
}

// S1: walk every /proc/<pid>/fdinfo/<fd>; count DRM clients belonging
// to OTHER processes. PASS needs >=1 foreign client (prove SYS_PTRACE
// + pid=host suffices — no privileged mode).
func runS1() bool {
	self := os.Getpid()
	clients, errs := 0, 0
	drivers := map[string]int{}

	pids, _ := filepath.Glob("/proc/[0-9]*")
	for _, pdir := range pids {
		var pid int
		fmt.Sscanf(filepath.Base(pdir), "%d", &pid)
		if pid == self {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(pdir, "fdinfo"))
		if err != nil {
			errs++
			continue
		}
		for _, fd := range fds {
			f, err := os.Open(filepath.Join(pdir, "fdinfo", fd.Name()))
			if err != nil {
				continue
			}
			info, ok := gpu.ParseFDInfo(f)
			f.Close()
			if !ok {
				continue
			}
			clients++
			drivers[info.Driver]++
			if clients <= 3 { // dump a few raw for fixture capture
				fmt.Printf("--- raw fdinfo (pid %d, driver %s) ---\n", pid, info.Driver)
				for k, v := range info.Fields {
					fmt.Printf("%s: %s\n", k, v)
				}
				if raw, err := os.ReadFile(filepath.Join(pdir, "cgroup")); err == nil {
					id, okc := cgroup.ContainerID(string(raw))
					fmt.Printf("cgroup container: %v %s\n", okc, id)
				}
			}
		}
	}
	if clients > 0 {
		fmt.Printf("S1 PASS: %d foreign DRM clients readable (drivers: %v; %d pids unreadable)\n", clients, drivers, errs)
		return true
	}
	fmt.Printf("S1 FAIL: no foreign DRM clients readable (%d pids unreadable) — is a GPU workload running? does the container have pid=host + SYS_PTRACE?\n", errs)
	return false
}

// S2: drop a test notification into the mounted spool. Human verifies
// it appears in the Unraid GUI / configured agents.
func runS2() bool {
	path, err := alert.WriteNotify("/notify", alert.Notification{
		Event:       "Gantry",
		Subject:     "Gantry spike S2",
		Description: "If you can read this in the Unraid GUI or your notification agent, S2 passes.",
		Importance:  "normal",
	}, time.Now())
	if err != nil {
		fmt.Printf("S2 FAIL: %v (is /tmp/notifications mounted rw at /notify?)\n", err)
		return false
	}
	fmt.Printf("S2 WROTE: %s — now confirm it shows in the Unraid GUI/agents (human step)\n", path)
	return true
}

// S3: find docker container cgroup dirs under /host/sys/fs/cgroup and
// read one cpu.stat.
func runS3() bool {
	root := "/host/sys/fs/cgroup"
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		fmt.Printf("S3 FAIL: %s/cgroup.controllers not readable (%v) — cgroup v1 box or mount missing; docker stats API fallback will be used\n", root, err)
		return false
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "docker", "*", "cpu.stat"))
	if len(dirs) == 0 {
		fmt.Printf("S3 FAIL: no docker/*/cpu.stat under %s — dump of %s/docker follows\n", root, root)
		entries, _ := os.ReadDir(filepath.Join(root, "docker"))
		for _, e := range entries {
			fmt.Println("  ", e.Name())
		}
		return false
	}
	body, err := os.ReadFile(dirs[0])
	if err != nil {
		fmt.Printf("S3 FAIL: found %d container cgroups but cpu.stat unreadable: %v\n", len(dirs), err)
		return false
	}
	fmt.Printf("S3 PASS: %d container cgroups; sample cpu.stat:\n%s", len(dirs), string(body))

	// PSI readability — the enabler for the Insights engine (spec §16).
	// Informational: S3 still passes without it, but record the verdict.
	dir := filepath.Dir(dirs[0])
	for _, name := range []string{"io.pressure", "cpu.pressure", "memory.pressure"} {
		if p, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Printf("S3 PSI: container %s readable:\n%s", name, string(p))
		} else {
			fmt.Printf("S3 PSI: container %s NOT readable (%v) — insights degrade to correlation-only\n", name, err)
		}
	}
	if p, err := os.ReadFile("/proc/pressure/io"); err == nil {
		fmt.Printf("S3 PSI: host /proc/pressure/io readable:\n%s", string(p))
	} else {
		fmt.Printf("S3 PSI: host /proc/pressure/io NOT readable (%v)\n", err)
	}
	return true
}
```

- [ ] **Step 2: Verify it builds (both platforms)**

```bash
go build -o /dev/null ./cmd/spikeprobe
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o spikeprobe-linux ./cmd/spikeprobe && rm spikeprobe-linux
go test ./... -race
```
Expected: builds clean, all tests still pass.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat(spike): spikeprobe harness for S1/S2/S3 wrapping production parsers"
```

---

### Task 18: ⛔ CHECKPOINT — GitHub repo + CI

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Makefile targets.
- Produces: `smidley/gantry` on GitHub with CI on push/PR.

- [ ] **Step 1: ⛔ Get Scott's go-ahead**

Orchestrator asks Scott: create `smidley/gantry` now, and public or private? (Spec says public eventually; private-until-v0.1.0 is a fine answer.) **Do not run `gh repo create` until answered.** If deferred, skip Steps 2–4, leave the workflow file committed locally, and continue to Task 19 (it has a no-GitHub path).

- [ ] **Step 2: Write CI workflow**

`.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"
      - run: make lint
      - run: go test ./... -race
      - run: make build
      - run: docker build -t gantry:ci .
```

Commit locally regardless of the checkpoint outcome:

```bash
git add -A && git commit -m "ci: lint, race tests, binary and image build on push/PR"
```

- [ ] **Step 3: Create repo and push (only after approval)**

```bash
gh repo create smidley/gantry --private --source /Users/scottbrant/Documents/gantry --push
```
(Swap `--private` for `--public` per Scott's answer.)

- [ ] **Step 4: Verify CI is green**

Run: `gh run watch --repo smidley/gantry --exit-status` (or `gh run list --repo smidley/gantry -L1`)
Expected: the ci workflow passes.

---

### Task 19: ⛔ CHECKPOINT — run spikes on the Unraid box, record results

**Files:**
- Create: `docs/superpowers/spikes.md`
- Possibly create: real fixture files under `internal/collect/gpu/testdata/` captured from the box

**Interfaces:**
- Consumes: `spikeprobe` (Task 17).
- Produces: recorded S1/S2/S3 verdicts — the decision gates for the Phase 2 plan (GPU field interpretation, notify format confirmation, cgroup fast path vs API fallback).

- [ ] **Step 1: ⛔ Get Scott's go-ahead**

This task touches Scott's server (192.168.1.50). Orchestrator confirms with Scott before deploying anything, and asks whether SSH (`ssh root@192.168.1.50`) is available or whether he'd rather run two pasted commands himself.

- [ ] **Step 2: Build and ship the probe**

Preferred (no registry needed — static binary straight to the box):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o spikeprobe ./cmd/spikeprobe
scp spikeprobe root@192.168.1.50:/tmp/spikeprobe
```

- [ ] **Step 3: Run it in a container with the exact Gantry template flags**

On the box (via ssh or pasted by Scott). The alpine wrapper matters — the probe must run *inside a container* to prove the containerized access model, not just that the data exists on the host:

```bash
docker run --rm --pid=host --cap-add=SYS_PTRACE \
  -v /tmp/spikeprobe:/spikeprobe:ro \
  -v /sys:/host/sys:ro \
  -v /var/local/emhttp:/unraid:ro \
  -v /tmp/notifications:/notify \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  alpine:3 /spikeprobe -all
```

For S1 to have something to see, a GPU workload should be active (e.g. play something transcoded in Jellyfin/Plex during the run).

- [ ] **Step 4: Verify S2 by hand**

Scott (or ssh check of `/tmp/notifications/archive/`) confirms the "Gantry spike S2" notification appeared in the Unraid GUI and was dispatched to his configured agent. If dynamix ignores the file, capture one of Unraid's own notify files (`ssh root@192.168.1.50 'cat /tmp/notifications/unread/*.notify /tmp/notifications/archive/*.notify 2>/dev/null | head -40'`) and adjust `internal/alert/notify.go` constants + tests to match, then re-run.

- [ ] **Step 5: Record results**

Write `docs/superpowers/spikes.md` with, per spike: verdict (PASS/FAIL), raw probe output (trimmed), decisions taken (e.g. "i915 exposes drm-engine-* ns counters → Phase 2 interprets ns deltas", "cgroup v2 confirmed → fast path default", "PSI readable → insights engine gets stall ground-truth"), and any parser/format changes made. Save at least one real fdinfo dump as a fixture file in `internal/collect/gpu/testdata/` (replacing hand-written guesses where they differ) and re-run `go test ./...`.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "docs: record S1/S2/S3 spike results from the unraid box"
```

---

## Phase 1 exit criteria

- `go test ./... -race` green; `make lint` clean.
- `make docker` produces a `scratch` image under 25MB that reports healthy and serves `/api/healthz` + placeholder page with fake data flowing.
- Spike verdicts recorded in `docs/superpowers/spikes.md` with real fixtures captured.
- (If approved) `smidley/gantry` on GitHub with green CI.

**Next:** write the Phase 2 plan (collectors), shaped by the spike results.
