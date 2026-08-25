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
