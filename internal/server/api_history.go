package server

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// TopRow is one leaderboard entry: an entity (container name) and its
// aggregated value for one metric — the shape Options.Top returns and the
// building block /api/top's resource-mapping sums into the wire response.
type TopRow struct {
	Entity string
	Value  float64
}

const (
	defaultSeriesWindow = 3600 // seconds; /api/series' "missing from/to -> last 1h"
	defaultTopLimit     = 10
	defaultEventsLimit  = 100
	maxEventsLimit      = 500

	// topSumFetchLimit is how many rows each underlying Top call fetches
	// when a resource sums multiple metrics (net, io, gpu) by entity before
	// re-sorting and cutting to the caller's requested limit. It must be
	// generous: an entity that leads on the SUM might not be in the top-N
	// of any single underlying metric, so under-fetching per metric could
	// silently drop it. Comfortably above any realistic container count.
	topSumFetchLimit = 10000
)

// parseInt64Param parses query param name as a base-10 int64. Absent ->
// (def, true). Present but unparseable -> (0, false); callers turn that
// into a 400.
func parseInt64Param(q url.Values, name string, def int64) (int64, bool) {
	v := q.Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// splitCSV splits a comma-separated query param into its parts, dropping
// empty entries (so "" and trailing/leading commas both degrade
// gracefully) — used for /api/series' metrics and /api/events' kinds. An
// absent/empty param yields nil, not [""].
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- /api/series -----------------------------------------------------------

type seriesResponseDTO struct {
	Metric string  `json:"metric"`
	Points [][]any `json:"points"` // [ts, avg, max] per point — arrays, not objects, for payload size
}

func toSeriesResponse(results []store.SeriesResult) []seriesResponseDTO {
	out := make([]seriesResponseDTO, len(results))
	for i, r := range results {
		pts := make([][]any, len(r.Points))
		for j, p := range r.Points {
			pts[j] = []any{p.TS, p.Avg, p.Max}
		}
		out[i] = seriesResponseDTO{Metric: r.Metric, Points: pts}
	}
	return out
}

// handleSeries serves /api/series?kind=&entity=&metrics=a,b,c&from=&to=.
// Options.Query is nil in tests that don't wire one — an empty JSON array
// is the harmless response in that case, matching every other optional
// closure's convention in this package.
func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().Unix()

	from, ok := parseInt64Param(q, "from", now-defaultSeriesWindow)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad from")
		return
	}
	to, ok := parseInt64Param(q, "to", now)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad to")
		return
	}

	if s.opts.Query == nil {
		writeJSON(w, []seriesResponseDTO{})
		return
	}
	results, err := s.opts.Query(r.Context(), q.Get("kind"), q.Get("entity"), splitCSV(q.Get("metrics")), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, toSeriesResponse(results))
}

// --- /api/top ----------------------------------------------------------

type topRowDTO struct {
	Entity string  `json:"entity"`
	Value  float64 `json:"value"`
}

func toTopResponse(rows []TopRow) []topRowDTO {
	out := make([]topRowDTO, len(rows))
	for i, r := range rows {
		out[i] = topRowDTO(r) // identical fields (ignoring json tags) -- a direct conversion
	}
	return out
}

// resourceMetrics maps a /api/top resource to the underlying metric name(s)
// to aggregate/sum by entity. The same mapping serves both the SQL-tier
// path (topFromStore, below) and the window="now" path (topFromSnapshot,
// summed straight from the current SnapshotDTO's per-container Metrics) —
// one source of truth for the resource->metric contract. gpu deliberately
// excludes gpu.nvidia.mem_mib (VRAM, not a busy percentage) from the
// engine-busy sum.
func resourceMetrics(resource string) ([]string, bool) {
	switch resource {
	case "cpu":
		return []string{"cpu.pct"}, true
	case "mem":
		return []string{"mem.bytes"}, true
	case "net":
		return []string{"net.rx_bps", "net.tx_bps"}, true
	case "io":
		return []string{"io.read_bps", "io.write_bps"}, true
	case "gpu":
		return []string{"gpu.render.busy_pct", "gpu.video.busy_pct", "gpu.video-enhance.busy_pct", "gpu.copy.busy_pct"}, true
	default:
		return nil, false
	}
}

// windowRange resolves a /api/top window enum. It's called for every
// window including "now" (so the enum itself is validated the same way
// regardless), but "now"'s returned (0,0) is never used -- the handler
// branches to topFromSnapshot for it instead, which needs no range at all.
func windowRange(window string, now int64) (from, to int64, ok bool) {
	switch window {
	case "now":
		return 0, 0, true
	case "1h":
		return now - 3600, now, true
	case "24h":
		return now - 86400, now, true
	case "7d":
		return now - 7*86400, now, true
	default:
		return 0, 0, false
	}
}

// topNFromSums ranks a per-entity sum map descending by value, breaking
// ties by entity name for deterministic output, and cuts to limit.
func topNFromSums(sums map[string]float64, limit int) []TopRow {
	out := make([]TopRow, 0, len(sums))
	for entity, value := range sums {
		out = append(out, TopRow{Entity: entity, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Entity < out[j].Entity
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// topFromStore serves every window except "now": a single-metric resource
// (cpu, mem) delegates straight through with the caller's own limit; a
// multi-metric resource (net, io, gpu) fetches each metric generously
// (topSumFetchLimit), sums by entity, then ranks and cuts to limit itself
// -- summing only each metric's own top-N would risk dropping an entity
// that leads on the combined total but not on any single term.
func (s *Server) topFromStore(ctx context.Context, metrics []string, from, to int64, agg string, limit int) ([]TopRow, error) {
	if s.opts.Top == nil {
		return nil, nil
	}
	if len(metrics) == 1 {
		return s.opts.Top(ctx, "container", metrics[0], from, to, agg, limit)
	}
	sums := map[string]float64{}
	for _, metric := range metrics {
		rows, err := s.opts.Top(ctx, "container", metric, from, to, agg, topSumFetchLimit)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			sums[row.Entity] += row.Value
		}
	}
	return topNFromSums(sums, limit), nil
}

// topFromSnapshot serves window="now": summed straight from the current
// SnapshotDTO's per-container Metrics rather than any store round-trip. A
// container is only included if at least one of the resource's metrics is
// actually present for it — a container with no GPU activity at all (or no
// sample yet) is left out rather than shown tied at the bottom with 0.
func (s *Server) topFromSnapshot(metrics []string, limit int) []TopRow {
	if s.opts.Snapshot == nil {
		return nil
	}
	dto := s.opts.Snapshot()
	sums := map[string]float64{}
	for entity, c := range dto.Containers {
		var sum float64
		var present bool
		for _, metric := range metrics {
			if v, ok := c.Metrics[metric]; ok {
				sum += v
				present = true
			}
		}
		if present {
			sums[entity] = sum
		}
	}
	return topNFromSums(sums, limit)
}

// handleTop serves /api/top?resource=cpu|mem|net|io|gpu&window=now|1h|24h|7d&agg=avg|peak&limit=.
// resource and window are required enums (no sensible default exists for
// either); agg defaults to "avg" and is validated for every window except
// "now", where it's ignored entirely (topFromSnapshot has no avg-vs-peak
// distinction — it reads the single current value).
func (s *Server) handleTop(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	metrics, ok := resourceMetrics(q.Get("resource"))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad resource")
		return
	}

	window := q.Get("window")
	now := time.Now().Unix()
	from, to, ok := windowRange(window, now)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad window")
		return
	}

	limit, ok := parseInt64Param(q, "limit", defaultTopLimit)
	if !ok || limit <= 0 {
		writeError(w, http.StatusBadRequest, "bad limit")
		return
	}

	agg := q.Get("agg")
	if window != "now" {
		switch agg {
		case "":
			agg = "avg"
		case "avg", "peak":
		default:
			writeError(w, http.StatusBadRequest, "bad agg")
			return
		}
	}

	var rows []TopRow
	var err error
	if window == "now" {
		rows = s.topFromSnapshot(metrics, int(limit))
	} else {
		rows, err = s.topFromStore(r.Context(), metrics, from, to, agg, int(limit))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, toTopResponse(rows))
}

// --- /api/events -----------------------------------------------------------

// handleEvents serves /api/events?kinds=a,b&entity=&from=&to=&limit=, a
// straight passthrough to store.QueryEvents via Options.Events. Unlike
// /api/series, absent from/to stay zero (EventFilter's own "no bound"
// value) rather than defaulting to a window. limit defaults to 100 and is
// capped at 500 regardless of what's requested.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	from, ok := parseInt64Param(q, "from", 0)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad from")
		return
	}
	to, ok := parseInt64Param(q, "to", 0)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad to")
		return
	}
	limit, ok := parseInt64Param(q, "limit", defaultEventsLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad limit")
		return
	}
	if limit <= 0 {
		limit = defaultEventsLimit
	}
	if limit > maxEventsLimit {
		limit = maxEventsLimit
	}

	f := store.EventFilter{
		Kinds:  splitCSV(q.Get("kinds")),
		Entity: q.Get("entity"),
		From:   from,
		To:     to,
		Limit:  int(limit),
	}

	if s.opts.Events == nil {
		writeJSON(w, []store.Event{})
		return
	}
	events, err := s.opts.Events(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []store.Event{}
	}
	writeJSON(w, events)
}
