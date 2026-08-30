package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeAlerts is a minimal in-memory AlertsIface double -- the same
// hand-rolled-fake convention fakeSettings uses in api_settings_test.go,
// just with enough bookkeeping (nextSilenceID, a save call log) to assert
// handleAlertsRulesPut's behavior without a real *store.Store. It has no
// need to model SaveRules' atomicity (a single Go-level assignment can't
// fail partway through the way a SQL transaction can) -- that guarantee
// is store.SaveAlertRules' own, pinned directly against a real *store.
// Store in internal/store/alerts_test.go.
type fakeAlerts struct {
	rules    []store.AlertRule
	active   []store.AlertInstance
	history  []store.AlertInstance
	silences []store.Silence
	channels map[string]string

	nextSilenceID int64

	rulesErr, activeErr, historyErr, silencesErr error
	saveErr                                      error
	addSilenceErr, deleteSilenceErr              error

	saveCalls   [][]store.AlertRule
	historyArgs struct {
		from, to int64
		limit    int
	}
}

func newFakeAlerts() *fakeAlerts {
	return &fakeAlerts{channels: map[string]string{}, nextSilenceID: 1}
}

func (f *fakeAlerts) Active(context.Context) ([]store.AlertInstance, error) {
	return f.active, f.activeErr
}

func (f *fakeAlerts) History(_ context.Context, from, to int64, limit int) ([]store.AlertInstance, error) {
	f.historyArgs.from, f.historyArgs.to, f.historyArgs.limit = from, to, limit
	return f.history, f.historyErr
}

func (f *fakeAlerts) Rules(context.Context) ([]store.AlertRule, error) { return f.rules, f.rulesErr }

// SaveRules mirrors store.SaveAlertRules' own doc: every Builtin row in
// rules is upserted in place (same id updates, a new id appends) and the
// entire non-builtin set is replaced wholesale by rules' non-builtin
// rows.
func (f *fakeAlerts) SaveRules(rules []store.AlertRule) error {
	f.saveCalls = append(f.saveCalls, rules)
	if f.saveErr != nil {
		return f.saveErr
	}
	for _, r := range rules {
		if !r.Builtin {
			continue
		}
		updated := false
		for i, er := range f.rules {
			if er.ID == r.ID {
				f.rules[i] = r
				updated = true
				break
			}
		}
		if !updated {
			f.rules = append(f.rules, r)
		}
	}
	kept := make([]store.AlertRule, 0, len(f.rules)+len(rules))
	for _, er := range f.rules {
		if er.Builtin {
			kept = append(kept, er)
		}
	}
	for _, r := range rules {
		if !r.Builtin {
			kept = append(kept, r)
		}
	}
	f.rules = kept
	return nil
}

func (f *fakeAlerts) Silences(context.Context) ([]store.Silence, error) {
	return f.silences, f.silencesErr
}

func (f *fakeAlerts) AddSilence(sil store.Silence) (store.Silence, error) {
	if f.addSilenceErr != nil {
		return store.Silence{}, f.addSilenceErr
	}
	sil.ID = f.nextSilenceID
	f.nextSilenceID++
	f.silences = append(f.silences, sil)
	return sil, nil
}

func (f *fakeAlerts) DeleteSilence(id int64) error {
	if f.deleteSilenceErr != nil {
		return f.deleteSilenceErr
	}
	out := f.silences[:0]
	for _, s := range f.silences {
		if s.ID != id {
			out = append(out, s)
		}
	}
	f.silences = out
	return nil
}

func (f *fakeAlerts) Channels() map[string]string { return f.channels }

// fakeWebhooks is a minimal in-memory WebhooksIface double.
type fakeWebhooks struct {
	targets    []alert.WebhookTarget
	envLocked  bool
	targetsErr error
	replaceErr error

	replaceCalls [][]alert.WebhookTarget
}

func (f *fakeWebhooks) Targets() ([]alert.WebhookTarget, bool, error) {
	return f.targets, f.envLocked, f.targetsErr
}

func (f *fakeWebhooks) Replace(targets []alert.WebhookTarget) error {
	f.replaceCalls = append(f.replaceCalls, targets)
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.targets = targets
	return nil
}

// doAlertsRequest issues one HTTP request with an optional JSON body --
// the shared helper every /api/alerts test below uses in place of a
// per-verb wrapper (api_settings_test.go's putSettings is PUT-only and
// already taken; this file also needs POST and DELETE).
func doAlertsRequest(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// marshalRules JSON-encodes a rules-PUT body from plain store.AlertRule
// values -- far less error-prone than hand-writing the two-dozen-field
// JSON literal for every test case.
func marshalRules(t *testing.T, rules ...store.AlertRule) string {
	t.Helper()
	dtos := make([]AlertRuleDTO, len(rules))
	for i, r := range rules {
		dtos[i] = AlertRuleDTO(r)
	}
	b, err := json.Marshal(alertRulesPutRequest{Rules: dtos})
	require.NoError(t, err)
	return string(b)
}

func testBuiltinRule(id string, threshold float64) store.AlertRule {
	return store.AlertRule{
		ID: id, Name: "Test " + id, Enabled: true, Builtin: true,
		Type: "threshold", Kind: "host", EntityGlob: "*",
		Metric: "cpu.total", Op: ">", Threshold: threshold, ClearThreshold: threshold - 10,
		Severity: "warning",
	}
}

func testUserRule(id string) store.AlertRule {
	return store.AlertRule{
		ID: id, Name: "User " + id, Enabled: true, Builtin: false,
		Type: "threshold", Kind: "container", EntityGlob: "*",
		Metric: "cpu.pct", Op: ">", Threshold: 90, ClearThreshold: 80,
		ForSeconds: 60, Severity: "warning",
	}
}

// --- GET /api/alerts ---------------------------------------------------

func TestAlertsGetNilOptionsReturnsEmptyNotPanic(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body alertsGetResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body.Active)
	require.Empty(t, body.Silences)
	require.Empty(t, body.Channels)
}

// TestAlertsGetFiltersToFiringAndFlagsSilenced pins two behaviors at
// once: a "pending" instance is engine bookkeeping, never user-facing
// (same principle the plan states for the frame block), so it must not
// appear in "active"; and an instance covered by a silence must carry
// silenced:true so a dimmed row in the UI can still be found.
func TestAlertsGetFiltersToFiringAndFlagsSilenced(t *testing.T) {
	fa := newFakeAlerts()
	fa.active = []store.AlertInstance{
		{ID: 1, RuleID: "host-cpu-high", Entity: "", State: "firing", Severity: "warning", Value: 90, Threshold: 85},
		{ID: 2, RuleID: "disk-temp-high", Entity: "disk3", State: "firing", Severity: "warning", Value: 60, Threshold: 55},
		{ID: 3, RuleID: "host-mem-high", Entity: "", State: "pending", Severity: "warning", Value: 80, Threshold: 85},
	}
	fa.silences = []store.Silence{{ID: 1, RuleID: "disk-temp-high", Entity: "disk3", Until: time.Now().Unix() + 3600}}
	fa.channels = map[string]string{"notify": "ok"}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body alertsGetResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Active, 2, "the pending instance must be excluded")
	byRule := map[string]AlertInstanceDTO{}
	for _, a := range body.Active {
		byRule[a.RuleID] = a
	}
	require.False(t, byRule["host-cpu-high"].Silenced)
	require.True(t, byRule["disk-temp-high"].Silenced)
	require.Len(t, body.Silences, 1)
	require.Equal(t, "ok", body.Channels["notify"])
}

func TestAlertsGetPropagatesStoreError(t *testing.T) {
	fa := newFakeAlerts()
	fa.activeErr = fmt.Errorf("db closed")
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- GET/PUT /api/alerts/rules ------------------------------------------

func TestAlertsRulesGetNilOptionsReturnsEmpty(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/rules")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body alertRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body.Rules)
}

func TestAlertsRulesGetReturnsConfiguredRules(t *testing.T) {
	fa := newFakeAlerts()
	fa.rules = []store.AlertRule{testBuiltinRule("host-cpu-high", 85)}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/rules")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body alertRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Rules, 1)
	require.Equal(t, "host-cpu-high", body.Rules[0].ID)
	require.Equal(t, 85.0, body.Rules[0].Threshold)
}

// TestAlertsRulesGetDefaultsParamBypassesStore pins Task 11's "reset to
// default" contract: ?defaults=1 answers from the compiled-in seed list,
// not whatever is (or isn't) wired as Options.Alerts, so the UI never
// hardcodes the Task 5 defaults table itself.
func TestAlertsRulesGetDefaultsParamBypassesStore(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Alerts left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/rules?defaults=1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body alertRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, len(store.DefaultAlertRules()), len(body.Rules))
	require.True(t, body.Rules[0].Builtin)
}

func TestAlertsRulesPutNilOptionsReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAlertsRulesPutRejectsUnknownField(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", `{"rules":[],"bogus":1}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls)
}

// TestAlertsRulesPutUserRuleCRUDRoundTrips is the plain, no-builtins-
// involved whole-document-replace path: submit two user rules, read
// them back.
func TestAlertsRulesPutUserRuleCRUDRoundTrips(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := marshalRules(t, testUserRule("user-a"), testUserRule("user-b"))
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", body)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out alertRulesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out.Rules, 2)
	require.Len(t, fa.saveCalls, 1, "the whole submission must reach SaveRules in one call")
	require.Len(t, fa.saveCalls[0], 2)

	// UpdatedAt is server-assigned, never trusted from the client.
	for _, r := range out.Rules {
		require.NotZero(t, r.UpdatedAt)
	}
}

// TestAlertsRulesPutBuiltinEditAllowed pins that a builtin id resubmitted
// with Builtin:true and a changed threshold reaches SaveRules (whose
// upsert half applies it), and the edit is visible on the very next GET.
func TestAlertsRulesPutBuiltinEditAllowed(t *testing.T) {
	fa := newFakeAlerts()
	fa.rules = []store.AlertRule{testBuiltinRule("host-cpu-high", 85)}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	edited := testBuiltinRule("host-cpu-high", 90) // threshold 85 -> 90
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, edited))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fa.saveCalls, 1)
	require.Equal(t, 90.0, fa.saveCalls[0][0].Threshold)

	getResp, err := http.Get(ts.URL + "/api/alerts/rules")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var out alertRulesResponse
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&out))
	require.Len(t, out.Rules, 1)
	require.Equal(t, 90.0, out.Rules[0].Threshold)
	require.True(t, out.Rules[0].Builtin)
}

// TestAlertsRulesPutBuiltinTamperReturns400NotServerError pins the
// store-review carry-forward this task named explicitly: a submitted
// rule whose id matches an existing BUILTIN but whose own Builtin field
// reads false must 400 at the API layer, never reach SaveRules (whose
// plain INSERT would otherwise collide with alert_rules' PRIMARY KEY and
// surface as an opaque 500).
func TestAlertsRulesPutBuiltinTamperReturns400NotServerError(t *testing.T) {
	fa := newFakeAlerts()
	fa.rules = []store.AlertRule{testBuiltinRule("host-cpu-high", 85)}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	tampered := testBuiltinRule("host-cpu-high", 85)
	tampered.Builtin = false
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, tampered))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls, "a tampered request must be rejected before any write")

	var errBody struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	require.Contains(t, errBody.Error, "host-cpu-high")
}

// TestAlertsRulesPutOmittingBuiltinReturns400 pins "builtins are
// disable-only": dropping an existing builtin from the submitted list
// entirely (rather than just disabling it) must 400 naming it, not
// silently delete it.
func TestAlertsRulesPutOmittingBuiltinReturns400(t *testing.T) {
	fa := newFakeAlerts()
	fa.rules = []store.AlertRule{testBuiltinRule("host-cpu-high", 85), testBuiltinRule("host-mem-high", 85)}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Only resubmit one of the two existing builtins.
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, testBuiltinRule("host-cpu-high", 85)))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls)

	var errBody struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	require.Contains(t, errBody.Error, "host-mem-high")
}

// TestAlertsRulesPutClaimingUnknownBuiltinReturns400 is the mirror of
// the tamper case: a client cannot invent a new builtin out of thin air
// either.
func TestAlertsRulesPutClaimingUnknownBuiltinReturns400(t *testing.T) {
	fa := newFakeAlerts() // no existing rules at all
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, testBuiltinRule("not-a-real-builtin", 85)))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls)
}

func TestAlertsRulesPutValidationErrorReturns400WithValidatorMessage(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	bad := testUserRule("user-a")
	bad.Type = "not-a-real-type"
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, bad))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls)

	var errBody struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	// alert.ValidateRule's own message names the bad type -- pinning that
	// this IS the validator's message, not a locally reworded one.
	require.Contains(t, errBody.Error, "not-a-real-type")
}

func TestAlertsRulesPutRejectsDuplicateIDs(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, testUserRule("dup"), testUserRule("dup")))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls)
}

func TestAlertsRulesPutRejectsTooManyRules(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	rules := make([]store.AlertRule, maxAlertRules+1)
	for i := range rules {
		rules[i] = testUserRule(fmt.Sprintf("user-%d", i))
	}
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, rules...))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.saveCalls)
}

// --- GET /api/alerts/history ---------------------------------------------

func TestAlertsHistoryNilOptionsReturnsEmptyArray(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/history")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out []AlertInstanceDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Empty(t, out)
}

func TestAlertsHistoryDefaultAndCappedLimit(t *testing.T) {
	fa := newFakeAlerts()
	fa.history = []store.AlertInstance{{ID: 1, RuleID: "r", State: "resolved", ResolvedAt: 100}}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/history")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int64(0), fa.historyArgs.from)
	require.Equal(t, int64(0), fa.historyArgs.to)
	require.Equal(t, defaultAlertHistoryLimit, fa.historyArgs.limit)

	var out []AlertInstanceDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out, 1)
	require.False(t, out[0].Silenced, "a resolved instance's silenced flag is not meaningful and must default false")

	resp2, err := http.Get(ts.URL + "/api/alerts/history?limit=99999")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	require.Equal(t, maxAlertHistoryLimit, fa.historyArgs.limit, "an over-cap request must be clamped, not rejected")

	resp3, err := http.Get(ts.URL + "/api/alerts/history?from=10&to=20&limit=5")
	require.NoError(t, err)
	defer func() { _ = resp3.Body.Close() }()
	require.Equal(t, int64(10), fa.historyArgs.from)
	require.Equal(t, int64(20), fa.historyArgs.to)
	require.Equal(t, 5, fa.historyArgs.limit)
}

func TestAlertsHistoryBadQueryParamsReturn400(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, q := range []string{"from=abc", "to=abc", "limit=abc"} {
		resp, err := http.Get(ts.URL + "/api/alerts/history?" + q)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, q)
		_ = resp.Body.Close()
	}
}

// --- silences ------------------------------------------------------------

func TestAlertsSilencesPostRoundTripsThroughGetThenDelete(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPost, ts.URL+"/api/alerts/silences",
		`{"rule_id":"disk-temp-high","entity":"disk3","hours":8,"reason":"known hot week"}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created SilenceDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotZero(t, created.ID)
	require.Equal(t, "disk-temp-high", created.RuleID)
	require.Greater(t, created.Until, time.Now().Unix())
	require.Empty(t, created.Scope, "a rule/entity-scoped silence must not be labeled scope:all")

	getResp, err := http.Get(ts.URL + "/api/alerts")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var body alertsGetResponse
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&body))
	require.Len(t, body.Silences, 1)
	require.Empty(t, body.Silences[0].Scope)

	delResp := doAlertsRequest(t, http.MethodDelete, fmt.Sprintf("%s/api/alerts/silences/%d", ts.URL, created.ID), "")
	defer func() { _ = delResp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, delResp.StatusCode)

	getResp2, err := http.Get(ts.URL + "/api/alerts")
	require.NoError(t, err)
	defer func() { _ = getResp2.Body.Close() }()
	var body2 alertsGetResponse
	require.NoError(t, json.NewDecoder(getResp2.Body).Decode(&body2))
	require.Empty(t, body2.Silences)
}

// TestAlertsSilencesPostBothEmptyWithoutScopeReturns400 pins the fix-round
// policy decision: rule_id and entity both "" means "every rule, every
// entity" (engine.go's silenced() reads it that way, unchanged) -- a mute
// broad enough that a client must ask for it on purpose. Omitting scope
// entirely used to 200 and silently mute everything for up to 30 days;
// now it 400s naming the gesture the client needs to add.
func TestAlertsSilencesPostBothEmptyWithoutScopeReturns400(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPost, ts.URL+"/api/alerts/silences", `{"hours":8}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fa.silences, "a rejected request must not reach AddSilence")

	var errBody struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	require.Contains(t, errBody.Error, `"scope"`, `the 400 must name the "scope":"all" gesture it wants`)
}

// TestAlertsSilencesPostScopeAllReturns200AndLabelsDTO pins the other
// half: scope:"all" alongside both-empty rule_id/entity is accepted, and
// the response DTO carries scope:"all" so the UI can render it distinctly
// from an ordinary scoped silence. The store representation is untouched
// -- no migration, no new column -- rule_id="" + entity="" already meant
// "global" before this fix-round; AddSilence still receives exactly that,
// proven here by reading it back off the fake's own store.
func TestAlertsSilencesPostScopeAllReturns200AndLabelsDTO(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPost, ts.URL+"/api/alerts/silences", `{"hours":8,"scope":"all"}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created SilenceDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Equal(t, "all", created.Scope)
	require.Empty(t, created.RuleID)
	require.Empty(t, created.Entity)

	require.Len(t, fa.silences, 1)
	require.Empty(t, fa.silences[0].RuleID, "the stored row stays rule_id=\"\" -- no new column, no migration")
	require.Empty(t, fa.silences[0].Entity)
}

func TestAlertsSilencesDeleteNonexistentStillReturns204(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodDelete, ts.URL+"/api/alerts/silences/999", "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestAlertsSilencesDeleteBadIDReturns400(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodDelete, ts.URL+"/api/alerts/silences/not-a-number", "")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAlertsSilencesPostRejectsOutOfRangeHours(t *testing.T) {
	fa := newFakeAlerts()
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, hours := range []string{"0", "721", "-1"} {
		resp := doAlertsRequest(t, http.MethodPost, ts.URL+"/api/alerts/silences", `{"hours":`+hours+`}`)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, hours)
		_ = resp.Body.Close()
	}
	require.Empty(t, fa.silences)
}

func TestAlertsSilencesPostNilOptionsReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPost, ts.URL+"/api/alerts/silences", `{"hours":8}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- webhooks --------------------------------------------------------------

func TestAlertsWebhooksGetNilOptionsReturnsEmpty(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/webhooks")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out webhooksGetResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Empty(t, out.Targets)
}

// TestAlertsWebhooksGetNeverLeaksHeaderValue is the grep-level secret
// check: a configured secret header value must not appear ANYWHERE in
// the raw response bytes, in place of just checking the JSON field is
// absent (which wouldn't catch it leaking into a different field).
func TestAlertsWebhooksGetNeverLeaksHeaderValue(t *testing.T) {
	const secret = "sk-super-secret-token-xyz"
	fw := &fakeWebhooks{targets: []alert.WebhookTarget{
		{ID: "home", Name: "Home", URL: "https://example.com/hook", Enabled: true,
			HeaderName: "Authorization", HeaderValue: secret, TimeoutS: 10},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/alerts/webhooks")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret)
	require.Contains(t, string(raw), `"header_set":true`)

	var out webhooksGetResponse
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Len(t, out.Targets, 1)
	require.True(t, out.Targets[0].HeaderSet)
	require.Equal(t, "Authorization", out.Targets[0].HeaderName)
}

func TestAlertsWebhooksPutOmittedHeaderValueKeepsStoredSecret(t *testing.T) {
	const secret = "sk-keep-me"
	fw := &fakeWebhooks{targets: []alert.WebhookTarget{
		{ID: "home", Name: "Home", URL: "https://example.com/hook", Enabled: true,
			HeaderName: "Authorization", HeaderValue: secret, TimeoutS: 10},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Rename the target without touching header_value at all.
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks",
		`{"targets":[{"id":"home","name":"Home Renamed","url":"https://example.com/hook","enabled":true,"header_name":"Authorization","timeout_s":10}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fw.replaceCalls, 1)
	require.Equal(t, secret, fw.replaceCalls[0][0].HeaderValue, "omitting header_value must keep the existing secret")
	require.Equal(t, "Home Renamed", fw.targets[0].Name)
}

func TestAlertsWebhooksPutEmptyHeaderValueClearsSecret(t *testing.T) {
	fw := &fakeWebhooks{targets: []alert.WebhookTarget{
		{ID: "home", Name: "Home", URL: "https://example.com/hook", Enabled: true,
			HeaderName: "Authorization", HeaderValue: "sk-old", TimeoutS: 10},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks",
		`{"targets":[{"id":"home","name":"Home","url":"https://example.com/hook","enabled":true,"header_name":"Authorization","header_value":"","timeout_s":10}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "", fw.targets[0].HeaderValue)

	var out webhooksGetResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.False(t, out.Targets[0].HeaderSet)
}

func TestAlertsWebhooksPutRejectsInvalidTarget(t *testing.T) {
	fw := &fakeWebhooks{}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// file:// scheme -- alert.ValidateWebhookTarget rejects anything but http/https.
	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks",
		`{"targets":[{"id":"bad","name":"Bad","url":"file:///etc/passwd","enabled":true,"timeout_s":10}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fw.replaceCalls)
}

func TestAlertsWebhooksPutEnvLockedDifferingURLReturns409(t *testing.T) {
	fw := &fakeWebhooks{envLocked: true, targets: []alert.WebhookTarget{
		{ID: "env", Name: "Environment", URL: "https://env.example.com/hook", Enabled: true, TimeoutS: 10},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks",
		`{"targets":[{"id":"env","name":"Environment","url":"https://different.example.com/hook","enabled":true,"timeout_s":10}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Empty(t, fw.replaceCalls)
}

func TestAlertsWebhooksPutEnvLockedRemovalReturns409(t *testing.T) {
	fw := &fakeWebhooks{envLocked: true, targets: []alert.WebhookTarget{
		{ID: "env", Name: "Environment", URL: "https://env.example.com/hook", Enabled: true, TimeoutS: 10},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks", `{"targets":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Empty(t, fw.replaceCalls)
}

// TestAlertsWebhooksPutEnvLockedIdenticalWriteIsAllowed pins the other
// half of the settings-style contract: resubmitting the env target
// UNCHANGED (only editing something env doesn't control, like the
// header) is not a conflict.
func TestAlertsWebhooksPutEnvLockedIdenticalWriteIsAllowed(t *testing.T) {
	fw := &fakeWebhooks{envLocked: true, targets: []alert.WebhookTarget{
		{ID: "env", Name: "Environment", URL: "https://env.example.com/hook", Enabled: true, TimeoutS: 10},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks",
		`{"targets":[{"id":"env","name":"Environment","url":"https://env.example.com/hook","enabled":true,"header_name":"X-Api-Key","header_value":"newkey","timeout_s":10}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fw.replaceCalls, 1)
	require.Equal(t, "newkey", fw.replaceCalls[0][0].HeaderValue)
}

func TestAlertsWebhooksPutNilOptionsReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks", `{"targets":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAlertsWebhooksPutRejectsUnknownField(t *testing.T) {
	fw := &fakeWebhooks{}
	s := New(Options{Version: "test-1", Started: time.Now(), Webhooks: fw})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks", `{"targets":[],"bogus":1}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fw.replaceCalls)
}

// --- read-only mode asymmetry ---------------------------------------------

// TestReadOnlyModeBlocksWebhooksPutButNotRulesPut pins the plan's
// deliberate asymmetry (Global Constraints + Open question 3) in one
// test: GANTRY_READ_ONLY gates webhook-target writes only (an outbound
// side-effect capability); rule writes are config, same as /api/settings
// and /api/groups, and stay open.
func TestReadOnlyModeBlocksWebhooksPutButNotRulesPut(t *testing.T) {
	fa := newFakeAlerts()
	fw := &fakeWebhooks{}
	s := New(Options{Version: "test-1", Started: time.Now(), Alerts: fa, Webhooks: fw, ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	webhookResp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/webhooks", `{"targets":[]}`)
	defer func() { _ = webhookResp.Body.Close() }()
	require.Equal(t, http.StatusForbidden, webhookResp.StatusCode)
	require.Empty(t, fw.replaceCalls)

	rulesResp := doAlertsRequest(t, http.MethodPut, ts.URL+"/api/alerts/rules", marshalRules(t, testUserRule("user-a")))
	defer func() { _ = rulesResp.Body.Close() }()
	require.Equal(t, http.StatusOK, rulesResp.StatusCode, "rule writes are config-shaped and must stay open under READ_ONLY")

	silenceResp := doAlertsRequest(t, http.MethodPost, ts.URL+"/api/alerts/silences", `{"rule_id":"host-cpu-high","hours":1}`)
	defer func() { _ = silenceResp.Body.Close() }()
	require.Equal(t, http.StatusOK, silenceResp.StatusCode, "silence writes are config-shaped and must stay open under READ_ONLY")
}
