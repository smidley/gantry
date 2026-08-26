package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// --- /api/series ---------------------------------------------------------

// capturingQuery returns a fake Options.Query that records every call's
// arguments (for asserting param parsing/defaulting) and answers with one
// fixed SeriesResult per requested metric.
func capturingQuery(calls *[]struct {
	Kind, Entity string
	Metrics      []string
	From, To     int64
}) func(context.Context, string, string, []string, int64, int64) ([]store.SeriesResult, error) {
	return func(_ context.Context, kind, entity string, metrics []string, from, to int64) ([]store.SeriesResult, error) {
		*calls = append(*calls, struct {
			Kind, Entity string
			Metrics      []string
			From, To     int64
		}{kind, entity, metrics, from, to})
		out := make([]store.SeriesResult, len(metrics))
		for i, m := range metrics {
			out[i] = store.SeriesResult{Metric: m, Points: []store.SeriesPoint{{TS: 1000, Avg: 1.5, Max: 2.5}}}
		}
		return out, nil
	}
}

func TestSeriesEndpointDelegatesParsedParams(t *testing.T) {
	var calls []struct {
		Kind, Entity string
		Metrics      []string
		From, To     int64
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Query: capturingQuery(&calls)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/series?kind=container&entity=web&metrics=cpu.pct,mem.bytes&from=100&to=200")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, calls, 1)
	require.Equal(t, "container", calls[0].Kind)
	require.Equal(t, "web", calls[0].Entity)
	require.Equal(t, []string{"cpu.pct", "mem.bytes"}, calls[0].Metrics)
	require.Equal(t, int64(100), calls[0].From)
	require.Equal(t, int64(200), calls[0].To)

	var body []struct {
		Metric string       `json:"metric"`
		Points [][3]float64 `json:"points"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body, 2)
	require.Equal(t, "cpu.pct", body[0].Metric)
	require.Equal(t, [][3]float64{{1000, 1.5, 2.5}}, body[0].Points, "points must serialize as [ts,avg,max] arrays, not objects")
}

func TestSeriesEndpointMissingFromToDefaultsToLastHour(t *testing.T) {
	var calls []struct {
		Kind, Entity string
		Metrics      []string
		From, To     int64
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Query: capturingQuery(&calls)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	before := time.Now().Unix()
	resp, err := http.Get(ts.URL + "/api/series?kind=host&metrics=cpu.total")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	after := time.Now().Unix()

	require.Len(t, calls, 1)
	require.InDelta(t, 3600, calls[0].To-calls[0].From, 1, "missing from/to must default to a 1h window")
	require.GreaterOrEqual(t, calls[0].To, before)
	require.LessOrEqual(t, calls[0].To, after)
}

func TestSeriesEndpointBadFromReturns400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/series?kind=host&metrics=cpu.total&from=notanumber&to=200")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body["error"])
}

func TestSeriesEndpointBadToReturns400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/series?kind=host&metrics=cpu.total&from=100&to=notanumber")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSeriesEndpointEmptyArrayWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Query left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/series?kind=host&metrics=cpu.total")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body)
}

// --- /api/top -------------------------------------------------------------

// fakeTop builds an Options.Top that maps metric name -> rows to return,
// recording every metric it was asked for (so a test can assert exclusion,
// e.g. gpu.nvidia.mem_mib must never be requested). Rows are truncated to
// the requested limit, the same way the real store.TopEntities' SQL LIMIT
// would -- callers must supply byMetric rows pre-sorted descending.
func fakeTop(t *testing.T, byMetric map[string][]TopRow, requested *[]string) func(context.Context, string, string, int64, int64, string, int) ([]TopRow, error) {
	return func(_ context.Context, kind, metric string, from, to int64, agg string, limit int) ([]TopRow, error) {
		t.Helper()
		require.Equal(t, "container", kind, "resource endpoints are container-scoped today")
		if requested != nil {
			*requested = append(*requested, metric)
		}
		rows := byMetric[metric]
		if limit >= 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		return rows, nil
	}
}

func TestTopEndpointCPUDelegatesSingleMetric(t *testing.T) {
	var calls []struct {
		Metric   string
		From, To int64
		Agg      string
		Limit    int
	}
	top := func(_ context.Context, kind, metric string, from, to int64, agg string, limit int) ([]TopRow, error) {
		calls = append(calls, struct {
			Metric   string
			From, To int64
			Agg      string
			Limit    int
		}{metric, from, to, agg, limit})
		return []TopRow{{Entity: "web", Value: 42}}, nil
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=1h")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, calls, 1)
	require.Equal(t, "cpu.pct", calls[0].Metric)
	require.Equal(t, "avg", calls[0].Agg, "agg must default to avg")
	require.Equal(t, 10, calls[0].Limit, "limit must default to 10")
	require.InDelta(t, 3600, calls[0].To-calls[0].From, 1)

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []topRowDTO{{Entity: "web", Value: 42}}, body)
}

func TestTopEndpointMemMapsToMemBytes(t *testing.T) {
	var requested []string
	top := fakeTop(t, map[string][]TopRow{"mem.bytes": {{Entity: "db", Value: 900}}}, &requested)
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=mem&window=24h&agg=peak")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{"mem.bytes"}, requested)
}

// TestTopEndpointNetResourceSumsTwoMetricsByEntity is the dispatch's
// explicitly-named coverage requirement: net = rx_bps + tx_bps, summed by
// entity across two independent TopEntities calls, then re-sorted -- an
// entity that leads on tx but not rx must still surface correctly summed.
func TestTopEndpointNetResourceSumsTwoMetricsByEntity(t *testing.T) {
	var requested []string
	byMetric := map[string][]TopRow{
		"net.rx_bps": {{Entity: "web", Value: 100}, {Entity: "db", Value: 10}},
		"net.tx_bps": {{Entity: "db", Value: 200}, {Entity: "web", Value: 5}},
	}
	top := fakeTop(t, byMetric, &requested)
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=net&window=1h&limit=10")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.ElementsMatch(t, []string{"net.rx_bps", "net.tx_bps"}, requested)

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	// web: 100+5=105, db: 10+200=210 -- db must lead despite trailing on rx alone.
	require.Equal(t, []topRowDTO{{Entity: "db", Value: 210}, {Entity: "web", Value: 105}}, body)
}

func TestTopEndpointIoResourceSumsReadAndWrite(t *testing.T) {
	var requested []string
	byMetric := map[string][]TopRow{
		"io.read_bps":  {{Entity: "web", Value: 1000}},
		"io.write_bps": {{Entity: "web", Value: 500}},
	}
	top := fakeTop(t, byMetric, &requested)
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=io&window=7d")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.ElementsMatch(t, []string{"io.read_bps", "io.write_bps"}, requested)

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []topRowDTO{{Entity: "web", Value: 1500}}, body)
}

// TestTopEndpointGpuSumsFourEnginesExcludingNvidiaMem pins both halves of
// the gpu mapping: the four busy_pct engines are summed by entity, and
// gpu.nvidia.mem_mib is never requested at all (fakeTop's byMetric map has
// no entry for it -- if the handler asked for it anyway, that metric's
// zero-value/empty rows wouldn't be the failure signal, so we assert on the
// requested-metrics list directly instead).
func TestTopEndpointGpuSumsFourEnginesExcludingNvidiaMem(t *testing.T) {
	var requested []string
	byMetric := map[string][]TopRow{
		"gpu.render.busy_pct":        {{Entity: "plex", Value: 10}},
		"gpu.video.busy_pct":         {{Entity: "plex", Value: 20}},
		"gpu.video-enhance.busy_pct": {{Entity: "plex", Value: 5}},
		"gpu.copy.busy_pct":          {{Entity: "plex", Value: 1}},
	}
	top := fakeTop(t, byMetric, &requested)
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=gpu&window=24h")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.ElementsMatch(t, []string{
		"gpu.render.busy_pct", "gpu.video.busy_pct", "gpu.video-enhance.busy_pct", "gpu.copy.busy_pct",
	}, requested)
	require.NotContains(t, requested, "gpu.nvidia.mem_mib")

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []topRowDTO{{Entity: "plex", Value: 36}}, body)
}

func TestTopEndpointBadResourceReturns400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=bogus&window=1h")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTopEndpointBadWindowReturns400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=bogus")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTopEndpointBadAggReturns400ForNonNowWindow(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), Top: fakeTop(t, nil, nil)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=1h&agg=bogus")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTopEndpointBadLimitReturns400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=1h&limit=notanumber")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTopEndpointLimitCapsResults(t *testing.T) {
	byMetric := map[string][]TopRow{
		"cpu.pct": {{Entity: "a", Value: 3}, {Entity: "b", Value: 2}, {Entity: "c", Value: 1}},
	}
	top := fakeTop(t, byMetric, nil)
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=1h&limit=1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []topRowDTO{{Entity: "a", Value: 3}}, body)
}

// TestTopEndpointNowWindowUsesSnapshotNotTop pins the window="now"
// short-circuit: it must read Options.Snapshot's current frame and must
// NEVER call Top at all -- agg is also allowed to be garbage in this mode
// since it's ignored.
func TestTopEndpointNowWindowUsesSnapshotNotTop(t *testing.T) {
	top := func(context.Context, string, string, int64, int64, string, int) ([]TopRow, error) {
		t.Fatal("Top must not be called for window=now")
		return nil, nil
	}
	snapshot := func() SnapshotDTO {
		return SnapshotDTO{
			Containers: map[string]ContainerDTO{
				"web":  {Metrics: map[string]float64{"cpu.pct": 40}},
				"db":   {Metrics: map[string]float64{"cpu.pct": 75}},
				"idle": {Metrics: map[string]float64{"mem.bytes": 100}}, // no cpu.pct at all
			},
		}
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Top: top, Snapshot: snapshot})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=now&agg=not-a-real-agg")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []topRowDTO{{Entity: "db", Value: 75}, {Entity: "web", Value: 40}}, body,
		"idle (no cpu.pct sample yet) must be excluded, not shown at 0")
}

func TestTopEndpointNowWindowSumsNetFromSnapshot(t *testing.T) {
	snapshot := func() SnapshotDTO {
		return SnapshotDTO{
			Containers: map[string]ContainerDTO{
				"web": {Metrics: map[string]float64{"net.rx_bps": 10, "net.tx_bps": 5}},
			},
		}
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Snapshot: snapshot})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=net&window=now")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body []topRowDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []topRowDTO{{Entity: "web", Value: 15}}, body)
}

func TestTopEndpointEmptyArrayWhenTopNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Top left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/top?resource=cpu&window=1h")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body)
}

// --- /api/events -----------------------------------------------------------

func TestEventsEndpointDelegatesParsedFilter(t *testing.T) {
	var got store.EventFilter
	events := func(f store.EventFilter) ([]store.Event, error) {
		got = f
		return []store.Event{{ID: 1, TS: 100, Kind: "container", Entity: "web", Severity: "warning", Detail: "oom"}}, nil
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Events: events})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events?kinds=container,disk&entity=web&from=10&to=20&limit=5")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, []string{"container", "disk"}, got.Kinds)
	require.Equal(t, "web", got.Entity)
	require.Equal(t, int64(10), got.From)
	require.Equal(t, int64(20), got.To)
	require.Equal(t, 5, got.Limit)

	var body []store.Event
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body, 1)
	require.Equal(t, "oom", body[0].Detail)
}

func TestEventsEndpointDefaultLimitIs100(t *testing.T) {
	var got store.EventFilter
	events := func(f store.EventFilter) ([]store.Event, error) { got = f; return nil, nil }
	s := New(Options{Version: "test-1", Started: time.Now(), Events: events})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 100, got.Limit)
}

func TestEventsEndpointLimitCappedAt500(t *testing.T) {
	var got store.EventFilter
	events := func(f store.EventFilter) ([]store.Event, error) { got = f; return nil, nil }
	s := New(Options{Version: "test-1", Started: time.Now(), Events: events})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events?limit=10000")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 500, got.Limit, "limit must be capped at 500")
}

func TestEventsEndpointBadLimitReturns400(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events?limit=notanumber")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEventsEndpointEmptyArrayWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Events left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body)
}
