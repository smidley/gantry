package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const DefaultRingCap = 450 // 15 minutes at one sample per 2s

// Store is the front door to all Gantry persistence.
type Store struct {
	db     *sql.DB // single writer: MaxOpenConns(1)
	readDB *sql.DB // read pool: MaxOpenConns(4), for Phase 3's query API
	live   *Live
	clock  func() time.Time

	idMu sync.Mutex
	ids  map[SeriesKey]int64

	lastFlushed int64 // unix seconds of the last fully-flushed minute boundary
	// (FlushMinutes is called from a single goroutine — the wiring loop — so
	// lastFlushed needs no lock)
}

func Open(path string, clock func() time.Time) (*Store, error) {
	if clock == nil {
		clock = time.Now
	}
	db, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	readDB, err := openReadPool(path)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{
		db:     db,
		readDB: readDB,
		live:   NewLive(DefaultRingCap),
		clock:  clock,
		ids:    make(map[SeriesKey]int64),
	}, nil
}

// Close closes both the write handle and the read pool, joining any
// errors from either.
func (s *Store) Close() error {
	return errors.Join(s.db.Close(), s.readDB.Close())
}

func (s *Store) Live() *Live { return s.live }
func (s *Store) DB() *sql.DB { return s.db }

// ReadDB returns the read-pool handle (MaxOpenConns(4)) for concurrent
// queries that don't need the single-writer serialization DB() enforces —
// used by Phase 3's query API.
func (s *Store) ReadDB() *sql.DB { return s.readDB }

// Record satisfies MetricSink. Hot path: RAM only — SQLite is fed by
// the minute flusher, never per-tick.
func (s *Store) Record(key SeriesKey, ts int64, val float64) {
	s.live.Record(key, ts, val)
}

func (s *Store) seriesID(ctx context.Context, key SeriesKey) (int64, error) {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	if id, ok := s.ids[key]; ok {
		return id, nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO series (kind, entity, metric, created_at) VALUES (?,?,?,?)
		ON CONFLICT (kind, entity, metric) DO NOTHING`,
		key.Kind, key.Entity, key.Metric, s.clock().Unix())
	if err != nil {
		return 0, err
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM series WHERE kind=? AND entity=? AND metric=?`,
		key.Kind, key.Entity, key.Metric).Scan(&id); err != nil {
		return 0, err
	}
	s.ids[key] = id
	return id, nil
}

var _ MetricSink = (*Store)(nil)
