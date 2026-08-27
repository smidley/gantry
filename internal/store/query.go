package store

import (
	"context"
	"database/sql"
	"strings"
)

// liveRingRangeMax and liveRingRecency together gate QuerySeries' live-ring
// branch: a request is only eligible for the Live ring when its span is no
// wider than 15 minutes (the ring's own capacity, DefaultRingCap) AND its
// `to` end is within the last 30 seconds of now. Both matter independently
// -- a narrow-but-historical window (e.g. 5 minutes from 3 days ago) has a
// qualifying span but the ring has long since evicted that data, so it must
// fall through to samples_1m instead of silently returning nothing.
const (
	liveRingRangeMax = 15 * 60
	liveRingRecency  = 30
)

// Tier-table span boundaries, shared by QuerySeries' SQL path and
// TopEntities (which is SQL-only end to end -- see tierTable).
const (
	tier1mRangeMax  = 48 * 3600
	tier10mRangeMax = 30 * 24 * 3600
)

// SeriesPoint is one aggregated (or, for the live-ring path, instantaneous)
// sample: Avg==Max==Val for a live-ring point, since a single live sample
// carries no aggregation.
type SeriesPoint struct {
	TS       int64
	Avg, Max float64
}

// SeriesResult is one requested metric's history.
type SeriesResult struct {
	Metric string
	Points []SeriesPoint
}

// tierTable picks which downsample tier's table to query for a request
// spanning span seconds: <=48h -> samples_1m, <=30d -> samples_10m, else
// samples_1h. Used by QuerySeries' SQL path and by TopEntities, which never
// touches the Live ring at all -- a "now" window is the API layer's job
// (short-circuiting to the live snapshot before ever calling TopEntities).
func tierTable(span int64) string {
	switch {
	case span <= tier1mRangeMax:
		return "samples_1m"
	case span <= tier10mRangeMax:
		return "samples_10m"
	default:
		return "samples_1h"
	}
}

// lookupSeriesID resolves a series' id for read-only queries, on the read
// pool -- unlike the write-side seriesID helper, it never creates a series
// row, so a query can never race the writer or mutate state. ok is false
// (with a nil error) when the kind/entity/metric combination has never been
// recorded, the "unknown series" case QuerySeries and TopEntities both
// treat as an empty result, never an error.
func (s *Store) lookupSeriesID(ctx context.Context, kind, entity, metric string) (int64, bool, error) {
	var id int64
	err := s.readDB.QueryRowContext(ctx, `SELECT id FROM series WHERE kind=? AND entity=? AND metric=?`,
		kind, entity, metric).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// QuerySeries returns history for the requested metrics of one kind+entity
// series over [from, to). A request whose span is at most 15 minutes AND
// whose `to` end is within the last 30 seconds of now is served straight
// from the in-RAM Live ring (see liveRingRangeMax/liveRingRecency); every
// other request is served from SQL, tiered by total span (see tierTable).
// `live:`-prefixed metrics are internal-only (per-device docker rates) and
// are never served here -- like any metric this store has never recorded,
// they come back with empty Points, never an error. The returned slice has
// exactly one SeriesResult per requested metric, in request order, so a
// caller can zip metrics[i] with the result.
func (s *Store) QuerySeries(ctx context.Context, kind, entity string, metrics []string, from, to int64) ([]SeriesResult, error) {
	out := make([]SeriesResult, len(metrics))
	for i, metric := range metrics {
		out[i] = SeriesResult{Metric: metric}
	}

	span := to - from
	if span <= liveRingRangeMax && to >= s.clock().Unix()-liveRingRecency {
		for i, metric := range metrics {
			if strings.HasPrefix(metric, "live:") {
				continue
			}
			key := SeriesKey{Kind: kind, Entity: entity, Metric: metric}
			for _, samp := range s.live.Since(key, from) {
				if samp.TS >= to {
					continue
				}
				out[i].Points = append(out[i].Points, SeriesPoint{TS: samp.TS, Avg: samp.Val, Max: samp.Val})
			}
		}
		return out, nil
	}

	table := tierTable(span)
	for i, metric := range metrics {
		if strings.HasPrefix(metric, "live:") {
			continue
		}
		id, ok, err := s.lookupSeriesID(ctx, kind, entity, metric)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		points, err := s.queryTierPoints(ctx, table, id, from, to)
		if err != nil {
			return nil, err
		}
		out[i].Points = points
	}
	return out, nil
}

// queryTierPoints runs the ts-indexed range scan (WHERE series_id=? AND
// ts>=? AND ts<?) against one tier table for one already-resolved series id.
func (s *Store) queryTierPoints(ctx context.Context, table string, seriesID, from, to int64) ([]SeriesPoint, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT ts, avg, max FROM `+table+` WHERE series_id=? AND ts>=? AND ts<? ORDER BY ts`,
		seriesID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var points []SeriesPoint
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.TS, &p.Avg, &p.Max); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// TopEntities aggregates one metric across every entity of a kind over
// [from, to): agg "avg" -> AVG(avg), "peak" -> MAX(max), grouped by entity,
// ordered desc, capped at limit. The SQL tier is picked by requested span
// alone (tierTable) -- this is SQL-only end to end and never touches the
// Live ring; a "now" window is the API layer's job, short-circuiting to the
// live snapshot before ever calling this.
func (s *Store) TopEntities(ctx context.Context, kind, metric string, from, to int64, agg string, limit int) ([]struct {
	Entity string
	Value  float64
}, error) {
	aggExpr := "AVG(t.avg)"
	if agg == "peak" {
		aggExpr = "MAX(t.max)"
	}
	table := tierTable(to - from)

	// ORDER BY value DESC alone leaves ties in whatever order SQLite
	// happens to produce them (unspecified, not guaranteed stable); the
	// entity tiebreak makes the result deterministic end to end.
	rows, err := s.readDB.QueryContext(ctx, `SELECT s.entity, `+aggExpr+` AS value FROM `+table+` t
		JOIN series s ON s.id = t.series_id
		WHERE s.kind = ? AND s.metric = ? AND t.ts >= ? AND t.ts < ?
		GROUP BY s.entity
		ORDER BY value DESC, s.entity ASC
		LIMIT ?`,
		kind, metric, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []struct {
		Entity string
		Value  float64
	}
	for rows.Next() {
		var row struct {
			Entity string
			Value  float64
		}
		if err := rows.Scan(&row.Entity, &row.Value); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
