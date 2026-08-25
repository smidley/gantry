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
