package store

import (
	"context"
	"strings"
	"time"
)

const flushCatchUpMax = 15 // ring holds 15 minutes; older windows are gone anyway

// FlushMinutes writes 1-minute (avg, max) aggregates for every complete
// minute since the previous call. The first call only records a baseline.
func (s *Store) FlushMinutes(ctx context.Context, now time.Time) (int, error) {
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

	var buf []Sample // reused across every series and window this call touches
	for m := start; m < nowMin; m += 60 {
		type agg struct {
			key      SeriesKey
			sum, max float64
			count    int
		}
		var aggs []agg
		s.live.ForEach(func(key SeriesKey, ring *Ring) {
			if strings.HasPrefix(key.Metric, "live:") {
				return // per-device docker IO etc.: live ring only, never persisted
			}
			buf = ring.AppendSince(m, buf[:0])
			a := agg{key: key}
			for _, smp := range buf {
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
			// Resolve all series IDs before starting transaction to avoid connection deadlock
			type aggWithID struct {
				id       int64
				avg, max float64
			}
			var aggsWithID []aggWithID
			for _, a := range aggs {
				id, err := s.seriesID(ctx, a.key)
				if err != nil {
					return written, err
				}
				aggsWithID = append(aggsWithID, aggWithID{
					id:  id,
					avg: a.sum / float64(a.count),
					max: a.max,
				})
			}

			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return written, err
			}
			for _, a := range aggsWithID {
				if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO samples_1m (series_id, ts, avg, max)
					VALUES (?,?,?,?)`, a.id, m, a.avg, a.max); err != nil {
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
