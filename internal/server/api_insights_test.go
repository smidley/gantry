package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeInsights is a minimal in-memory InsightsIface double -- the exact
// fakeAlerts convention (api_alerts_test.go's own doc): enough
// bookkeeping (byID, dismissals, resolveCalls) to assert the handlers'
// behavior without a real *store.Store, whose own CRUD contracts (the
// partial unique index, StaleActiveInsights, ...) are pinned directly
// against sqlite in internal/store/insights_test.go instead.
type fakeInsights struct {
	active  []store.InsightInstance
	byID    map[int64]store.InsightInstance
	history []store.InsightInstance
	cfgs    []store.InsightRuleConfig
	tier    string
	dropped int

	activeErr, byIDErr, historyErr, cfgsErr error
	saveErr, addDismissalErr, resolveErr    error

	saveCalls      []store.InsightRuleConfig
	dismissalCalls []store.InsightDismissal
	resolveCalls   []struct {
		id     int64
		at     int64
		reason string
	}
}

func newFakeInsights() *fakeInsights {
	return &fakeInsights{byID: map[int64]store.InsightInstance{}, tier: "proxy"}
}

func (f *fakeInsights) Active(context.Context) ([]store.InsightInstance, error) {
	return f.active, f.activeErr
}

func (f *fakeInsights) ByID(_ context.Context, id int64) (store.InsightInstance, bool, error) {
	if f.byIDErr != nil {
		return store.InsightInstance{}, false, f.byIDErr
	}
	inst, ok := f.byID[id]
	return inst, ok, nil
}

func (f *fakeInsights) History(_ context.Context, _, _ int64, _ int) ([]store.InsightInstance, error) {
	return f.history, f.historyErr
}

func (f *fakeInsights) RuleConfigs(context.Context) ([]store.InsightRuleConfig, error) {
	return f.cfgs, f.cfgsErr
}

func (f *fakeInsights) SaveRuleConfig(c store.InsightRuleConfig) error {
	f.saveCalls = append(f.saveCalls, c)
	if f.saveErr != nil {
		return f.saveErr
	}
	for i, existing := range f.cfgs {
		if existing.RuleID == c.RuleID {
			f.cfgs[i] = c
			return nil
		}
	}
	f.cfgs = append(f.cfgs, c)
	return nil
}

func (f *fakeInsights) AddDismissal(d store.InsightDismissal) (int64, error) {
	f.dismissalCalls = append(f.dismissalCalls, d)
	return int64(len(f.dismissalCalls)), f.addDismissalErr
}

func (f *fakeInsights) Resolve(id, at int64, reason string) error {
	f.resolveCalls = append(f.resolveCalls, struct {
		id     int64
		at     int64
		reason string
	}{id, at, reason})
	if f.resolveErr != nil {
		return f.resolveErr
	}
	inst, ok := f.byID[id]
	if !ok {
		return fmt.Errorf("insight %d: not found", id)
	}
	inst.State, inst.ResolvedAt, inst.ResolveReason = "resolved", at, reason
	f.byID[id] = inst
	return nil
}

func (f *fakeInsights) Tier() string    { return f.tier }
func (f *fakeInsights) Suppressed() int { return f.dropped }

func fullInsightInstance(id int64, ruleID, victimKind, victim, culprit, resource string) store.InsightInstance {
	return store.InsightInstance{
		ID: id, RuleID: ruleID, VictimKind: victimKind, Victim: victim, Culprit: culprit,
		Resource: resource, State: "active", Severity: "warning", Confidence: "likely", Tier: "proxy",
		Statement: culprit + " is likely slowing " + resource,
		Evidence:  `{"CulpritSharePct":78,"DeviceUtilPct":98,"AwaitMs":42}`,
		StartedAt: 1756400000, FiredAt: 1756400600,
	}
}

// --- GET /api/insights ---------------------------------------------------

func TestInsightsGetNilOptionsReturnsEmptyNotPanic(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body InsightsBlockDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body.Active)
	require.Equal(t, "proxy", body.Tier)
}

// TestInsightsGetIncludesEvidenceUnlikeFrameBlock pins Task 9's own
// split: GET /api/insights carries evidence bundles (unlike the SSE
// frame's insights block, which never does -- see ToInsightDTO's own
// doc). A decoded evidence value proves the JSON round trip through
// insight.Evidence's untagged fields worked, not just that the key is
// present.
func TestInsightsGetIncludesEvidenceUnlikeFrameBlock(t *testing.T) {
	fi := newFakeInsights()
	fi.active = []store.InsightInstance{fullInsightInstance(1, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3")}
	fi.tier, fi.dropped = "psi", 2
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var body InsightsBlockDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Active, 1)
	require.NotNil(t, body.Active[0].Evidence)
	require.Equal(t, 78.0, body.Active[0].Evidence.CulpritSharePct)
	require.Equal(t, 98.0, body.Active[0].Evidence.DeviceUtilPct)
	require.Equal(t, "psi", body.Tier)
	require.Equal(t, 2, body.Suppressed)
}

func TestInsightsGetPropagatesStoreError(t *testing.T) {
	fi := newFakeInsights()
	fi.activeErr = fmt.Errorf("db closed")
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- GET /api/insights/{id} -----------------------------------------------

func TestInsightGetFindsActiveAndResolvedRows(t *testing.T) {
	fi := newFakeInsights()
	fi.byID[1] = fullInsightInstance(1, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3")
	resolved := fullInsightInstance(2, "memory-squeeze", "host", "", "plex", "memory")
	resolved.State, resolved.ResolvedAt, resolved.ResolveReason = "resolved", 1756402000, "cleared"
	fi.byID[2] = resolved
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var dto InsightDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Equal(t, "jellyfin", dto.Victim)
	require.NotNil(t, dto.Evidence)

	resp2, err := http.Get(ts.URL + "/api/insights/2")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	var dto2 InsightDTO
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&dto2))
	require.Equal(t, "resolved", dto2.State)
	require.Equal(t, "cleared", dto2.ResolveReason)
}

func TestInsightGetUnknownIDReturns404(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/999")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestInsightGetBadIDReturns400(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/not-a-number")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestInsightGetNilOptionsReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- GET /api/insights/history --------------------------------------------

func TestInsightsHistoryNilOptionsReturnsEmptyArray(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/history")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body []InsightDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body)
}

func TestInsightsHistoryReturnsResolvedWithEvidence(t *testing.T) {
	fi := newFakeInsights()
	resolved := fullInsightInstance(3, "disk-spinup-churn", "disk", "", "plex", "disk5")
	resolved.State, resolved.ResolvedAt, resolved.ResolveReason = "resolved", 1756402000, "cleared"
	fi.history = []store.InsightInstance{resolved}
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/history?limit=50")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body []InsightDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body, 1)
	require.NotNil(t, body[0].Evidence)
}

func TestInsightsHistoryBadQueryParamsReturn400(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, qs := range []string{"from=nope", "to=nope", "limit=nope"} {
		resp, err := http.Get(ts.URL + "/api/insights/history?" + qs)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, qs)
		_ = resp.Body.Close()
	}
}

// --- GET/PUT /api/insights/rules -----------------------------------------

func TestInsightsRulesGetMergesDefaultsAndOverrides(t *testing.T) {
	fi := newFakeInsights()
	fi.cfgs = []store.InsightRuleConfig{
		{RuleID: "disk-io-contention", Enabled: true, Notify: false, Overrides: `{"util_pct_floor":80}`, UpdatedAt: 111},
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/rules")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body insightRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Rules, 7, "all seven compiled-in rules must be listed regardless of which have a config row")

	byID := map[string]InsightRuleDTO{}
	for _, r := range body.Rules {
		byID[r.RuleID] = r
	}
	dio := byID["disk-io-contention"]
	require.Equal(t, 80.0, dio.Thresholds["util_pct_floor"], "the override must win in the effective set")
	require.Equal(t, 90.0, dio.Defaults["util_pct_floor"], "Defaults must stay the compiled-in value, unaffected by the override")
	require.Equal(t, int64(111), dio.UpdatedAt)

	// A rule with no config row at all still lists enabled -- see
	// buildInsightRuleDTOs' own doc on why the degrade-safe default is
	// true, not the zero value.
	require.True(t, byID["memory-squeeze"].Enabled)
}

func TestInsightsRulesPutRoundTripsThresholdsEnabledAndNotify(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := insightRulesPutRequest{Rules: []insightRuleInput{
		{RuleID: "disk-io-contention", Enabled: false, Notify: true, Overrides: map[string]float64{"util_pct_floor": 85}},
	}}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/insights/rules", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out insightRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	var dio InsightRuleDTO
	for _, r := range out.Rules {
		if r.RuleID == "disk-io-contention" {
			dio = r
		}
	}
	require.False(t, dio.Enabled)
	require.True(t, dio.Notify)
	require.Equal(t, 85.0, dio.Thresholds["util_pct_floor"])
	require.Len(t, fi.saveCalls, 1)
}

// TestInsightsRulesPutClearsOverridesWhenOmitted pins the whole-document-
// replace semantics: an omitted (or empty) overrides map on a
// resubmission must clear whatever was there before, not merge with it
// -- SaveRuleConfig always overwrites the Overrides column wholesale
// (store.UpsertInsightRuleConfig), never merges.
func TestInsightsRulesPutClearsOverridesWhenOmitted(t *testing.T) {
	fi := newFakeInsights()
	fi.cfgs = []store.InsightRuleConfig{{RuleID: "disk-io-contention", Enabled: true, Overrides: `{"util_pct_floor":85}`}}
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := insightRulesPutRequest{Rules: []insightRuleInput{{RuleID: "disk-io-contention", Enabled: true}}}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/insights/rules", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var out insightRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	for _, r := range out.Rules {
		if r.RuleID == "disk-io-contention" {
			require.Equal(t, 90.0, r.Thresholds["util_pct_floor"], "must fall back to the compiled-in default once the override is cleared")
		}
	}
	require.Equal(t, "", fi.saveCalls[0].Overrides)
}

func TestInsightsRulesPutRejectsUnknownField(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/insights/rules", bytes.NewReader(
		[]byte(`{"rules":[{"rule_id":"disk-io-contention","enabled":true,"notify":false,"metric":"cpu.total"}]}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a field naming a rule's shape (metric) has nowhere to decode into and must 400, not silently drop")
}

func TestInsightsRulesPutRejectsUnknownRuleID(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := insightRulesPutRequest{Rules: []insightRuleInput{{RuleID: "made-up-rule", Enabled: true}}}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/insights/rules", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestInsightsRulesPutRejectsDuplicateRuleID(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := insightRulesPutRequest{Rules: []insightRuleInput{
		{RuleID: "disk-io-contention", Enabled: true},
		{RuleID: "disk-io-contention", Enabled: false},
	}}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/insights/rules", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestInsightsRulesPutNilOptionsReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/insights/rules", bytes.NewReader([]byte(`{"rules":[]}`)))
	require.NoError(t, err)
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- POST /api/insights/{id}/dismiss ---------------------------------------

func TestInsightDismissCreatesDismissalAndResolvesFromTheInstancesOwnIdentity(t *testing.T) {
	fi := newFakeInsights()
	fi.byID[1] = fullInsightInstance(1, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3")
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/insights/1/dismiss", bytes.NewReader([]byte(`{"days":7}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, fi.dismissalCalls, 1)
	d := fi.dismissalCalls[0]
	require.Equal(t, "disk-io-contention", d.RuleID)
	require.Equal(t, "jellyfin", d.Victim)
	require.Equal(t, "qbittorrent", d.Culprit)
	require.Equal(t, "disk3", d.Resource)
	require.WithinDuration(t, time.Now().Add(7*24*time.Hour), time.Unix(d.Until, 0), 5*time.Second)

	require.Len(t, fi.resolveCalls, 1)
	require.Equal(t, "dismissed", fi.resolveCalls[0].reason)

	var dto InsightDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Equal(t, "resolved", dto.State)
	require.Equal(t, "dismissed", dto.ResolveReason)
}

func TestInsightDismissRejectsOutOfRangeDays(t *testing.T) {
	fi := newFakeInsights()
	fi.byID[1] = fullInsightInstance(1, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3")
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, days := range []int{0, 31, -1} {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/insights/1/dismiss", bytes.NewReader([]byte(fmt.Sprintf(`{"days":%d}`, days))))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(requestedWithHeader, requestedWithValue)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, days)
		_ = resp.Body.Close()
	}
	require.Empty(t, fi.dismissalCalls, "an out-of-range request must never reach AddDismissal")
}

func TestInsightDismissUnknownIDReturns404(t *testing.T) {
	fi := newFakeInsights()
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/insights/999/dismiss", bytes.NewReader([]byte(`{"days":1}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- GET /api/insights/graph -----------------------------------------------

func TestInsightsGraphNilOptionsReturnsEmptyGraph(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/graph")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body InsightGraphDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body.Nodes)
	require.Empty(t, body.Edges)
}

// TestBuildInsightGraphNamedVictimContainerGetsBothEdges pins the common
// PSI-confirmed shape: a culprit->resource edge AND a resource->victim
// edge, both endpoints real container/resource nodes.
func TestBuildInsightGraphNamedVictimContainerGetsBothEdges(t *testing.T) {
	inst := fullInsightInstance(1, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3")
	g := buildInsightGraph([]store.InsightInstance{inst})

	require.Len(t, g.Nodes, 3, "qbittorrent, disk3 (resource), jellyfin")
	require.Len(t, g.Edges, 2)
	var culpritEdge, victimEdge *GraphEdgeDTO
	for i := range g.Edges {
		switch g.Edges[i].Kind {
		case "culprit":
			culpritEdge = &g.Edges[i]
		case "victim":
			victimEdge = &g.Edges[i]
		}
	}
	require.NotNil(t, culpritEdge)
	require.Equal(t, "qbittorrent", culpritEdge.From)
	require.Equal(t, "resource:disk3", culpritEdge.To)
	require.NotNil(t, victimEdge)
	require.Equal(t, "resource:disk3", victimEdge.From)
	require.Equal(t, "jellyfin", victimEdge.To)
}

// TestBuildInsightGraphHostWideFindingHasNoVictimEdge pins the common
// tier-1 shape (disk-io-contention's own likely branch, io-driven-cpu-
// load, parity-slowdown, ...): Victim is "" because there is no single
// named victim container, only a culprit->resource edge.
func TestBuildInsightGraphHostWideFindingHasNoVictimEdge(t *testing.T) {
	inst := fullInsightInstance(2, "io-driven-cpu-load", "host", "", "sabnzbd", "cpu")
	g := buildInsightGraph([]store.InsightInstance{inst})

	require.Len(t, g.Edges, 1)
	require.Equal(t, "culprit", g.Edges[0].Kind)
	require.Equal(t, "sabnzbd", g.Edges[0].From)
	require.Equal(t, "resource:cpu", g.Edges[0].To)
}

// TestBuildInsightGraphGPUEngineVictimIsNotMistakenForAContainer is the
// regression test for the one non-obvious VictimKind shape:
// gpu-engine-contention's own Finding sets VictimKind "gpu" with Victim
// holding the bare engine name (e.g. "video"), NOT a container -- see
// GraphEdgeDTO's own doc. A naive "Victim != ”" gate would invent a
// fictitious "video" container node and a nonsensical
// resource:gpu:video -> video edge; this must never happen.
func TestBuildInsightGraphGPUEngineVictimIsNotMistakenForAContainer(t *testing.T) {
	inst := fullInsightInstance(3, "gpu-engine-contention", "gpu", "video", "jellyfin", "gpu:video")
	g := buildInsightGraph([]store.InsightInstance{inst})

	for _, n := range g.Nodes {
		require.NotEqual(t, "video", n.ID, "the bare engine name must never become its own node")
	}
	require.Len(t, g.Edges, 1, "only the culprit edge -- no victim edge for a non-container VictimKind")
	require.Equal(t, "culprit", g.Edges[0].Kind)
}

// TestBuildInsightGraphSharedCulpritFansIntoOneResourceNode pins Open
// question 2's shared-culprit shape: a comma-joined Culprits column
// produces one culprit edge PER NAME, all landing on the SAME resource
// node -- not a synthesized "and" node, not a dropped second name.
func TestBuildInsightGraphSharedCulpritFansIntoOneResourceNode(t *testing.T) {
	inst := fullInsightInstance(4, "disk-io-contention", "", "", "", "disk3")
	inst.Culprit, inst.Culprits = "", "qbittorrent,sabnzbd"
	g := buildInsightGraph([]store.InsightInstance{inst})

	require.Len(t, g.Nodes, 3, "qbittorrent, sabnzbd, disk3 -- one resource node shared by both fan-in edges")
	require.Len(t, g.Edges, 2)
	for _, e := range g.Edges {
		require.Equal(t, "resource:disk3", e.To)
		require.Equal(t, "culprit", e.Kind)
	}
}

// TestBuildInsightGraphDualRoleContainerIsNotDuplicated is the plan's
// own explicit test case (Task 14): a container that is a culprit in
// one insight and a victim in another must be placed ONCE, with both
// edges attached to that single node -- never duplicated, never a
// crash.
func TestBuildInsightGraphDualRoleContainerIsNotDuplicated(t *testing.T) {
	aSlowsB := fullInsightInstance(5, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3")
	cSlowsA := fullInsightInstance(6, "cpu-starvation", "container", "qbittorrent", "plex", "cpu")
	g := buildInsightGraph([]store.InsightInstance{aSlowsB, cSlowsA})

	var qbittorrentCount int
	for _, n := range g.Nodes {
		if n.ID == "qbittorrent" {
			qbittorrentCount++
		}
	}
	require.Equal(t, 1, qbittorrentCount, "qbittorrent (culprit in one insight, victim in the other) must appear exactly once")
	require.Len(t, g.Edges, 4)
}

// TestBuildInsightGraphIsDeterministicallyOrdered pins the plan's own
// "position is identity" premise at its data source: two calls over the
// identical (but insertion-shuffled) input must produce byte-identical
// node/edge ORDER, not just the same set -- Go map iteration order is
// otherwise random, which would make the frontend's stable-sort layout
// (mapLayout.ts) chase a moving target for no reason.
func TestBuildInsightGraphIsDeterministicallyOrdered(t *testing.T) {
	insts := []store.InsightInstance{
		fullInsightInstance(7, "disk-io-contention", "container", "jellyfin", "qbittorrent", "disk3"),
		fullInsightInstance(8, "io-driven-cpu-load", "host", "", "sabnzbd", "cpu"),
		fullInsightInstance(9, "memory-squeeze", "container", "sonarr", "plex", "memory"),
	}
	first := buildInsightGraph(insts)
	for i := 0; i < 20; i++ {
		got := buildInsightGraph(insts)
		require.Equal(t, first, got)
	}
}

func TestInsightsGraphPropagatesStoreError(t *testing.T) {
	fi := newFakeInsights()
	fi.activeErr = fmt.Errorf("db closed")
	s := New(Options{Version: "test-1", Started: time.Now(), Insights: fi})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/insights/graph")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}
