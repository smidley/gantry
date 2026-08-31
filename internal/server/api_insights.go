package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/insight"
	"github.com/smidley/gantry/internal/store"
)

const (
	defaultInsightHistoryLimit = 100
	maxInsightHistoryLimit     = 500

	// maxInsightRulesSubmission is PUT /api/insights/rules' own
	// defensive cap, the maxAlertRules/maxGroups precedent -- there are
	// only ever 7 compiled-in rule ids (insight.DefaultRules()), so the
	// per-id "known rule" + "no duplicates" checks below already bound a
	// WELL-FORMED submission to 7; this exists only to reject a
	// malformed client's oversized body before it's processed at all.
	maxInsightRulesSubmission = 50

	minInsightDismissDays = 1
	maxInsightDismissDays = 30
)

// --- InsightsIface ----------------------------------------------------

// InsightsIface is the minimal store+engine surface /api/insights and
// its sub-routes need -- main wires a small adapter over *store.Store
// plus the running *insight.Engine's Dropped() and the pressure
// collector's Tier(), the exact AlertsIface precedent (api_alerts.go's
// own doc) for keeping this package store/engine-shape-agnostic. Nil in
// tests that don't wire one: every GET route reports its own meaningful
// empty; every write route 404s, matching Alerts' own convention.
type InsightsIface interface {
	// Active returns every instance with resolved_at = 0, for GET
	// /api/insights and the interaction map's GET /api/insights/graph.
	Active(ctx context.Context) ([]store.InsightInstance, error)
	// ByID returns one instance regardless of active/resolved state, for
	// GET /api/insights/{id} (the evidence drawer's own fetch) and the
	// dismiss route, which needs the target's rule/victim/culprit/
	// resource identity before it can record a dismissal against it. ok
	// is false for an id that doesn't exist.
	ByID(ctx context.Context, id int64) (store.InsightInstance, bool, error)
	// History returns resolved instances in [from,to] (either 0 = no
	// bound), newest resolution first, capped at limit, for GET
	// /api/insights/history.
	History(ctx context.Context, from, to int64, limit int) ([]store.InsightInstance, error)
	// RuleConfigs returns every rule's current enable/notify/overrides
	// row, for GET /api/insights/rules.
	RuleConfigs(ctx context.Context) ([]store.InsightRuleConfig, error)
	// SaveRuleConfig upserts one rule's config row -- PUT /api/insights/
	// rules calls this once per submitted rule (there is no builtin/
	// non-builtin split here, unlike alerts: every insight rule is
	// always compiled-in, so there's nothing for a "replace the
	// non-builtin set" second half to do).
	SaveRuleConfig(c store.InsightRuleConfig) error
	// AddDismissal records one "this wasn't useful" suppression window
	// for POST /api/insights/{id}/dismiss.
	AddDismissal(d store.InsightDismissal) (int64, error)
	// Resolve closes out an instance -- shared by the dismiss route
	// (reason "dismissed") with store.ResolveInsight's own contract.
	Resolve(id, at int64, reason string) error
	// Tier reports which pressure evidence family is currently live --
	// "psi" or "proxy" (pressure.Collector.Tier()) -- surfaced on GET
	// /api/insights and the frame's insights.tier so the UI's empty
	// state and PSI-upgrade copy know which to show without guessing
	// from a banner string.
	Tier() string
	// Suppressed reports how many findings the engine's global cap
	// (Task 7) has discarded across its lifetime (*insight.Engine.
	// Dropped()) -- "N insights suppressed by the active-findings cap"
	// in the frame and GET /api/insights.
	Suppressed() int
}

// --- wire DTOs ----------------------------------------------------------

// EvidenceDTO is insight.Evidence's wire shape: an IDENTICAL field-for-
// field mirror (same names, order, and types -- insight.Evidence itself
// carries no json tags, since it's an internal, not-yet-wire-exposed
// struct) so ToEvidenceDTO below is a plain Go type conversion, the same
// AlertRuleDTO/store.AlertRule idiom api_alerts.go's own doc names. Not
// every field applies to every rule/shape/confidence combination --
// insight.Evidence's own doc covers which; an unused field is simply
// its zero value here too, and the UI's evidence drawer renders only
// the numbers a given rule's Statement actually quoted.
type EvidenceDTO struct {
	CulpritSharePct   float64  `json:"culprit_share_pct"`
	DeviceUtilPct     float64  `json:"device_util_pct"`
	AwaitMs           float64  `json:"await_ms"`
	VictimStallPct    float64  `json:"victim_stall_pct"`
	WindowMinutes     int      `json:"window_minutes"`
	OtherUsers        []string `json:"other_users"`
	IowaitPct         float64  `json:"iowait_pct"`
	HostCPUPct        float64  `json:"host_cpu_pct"`
	SpinCount         int      `json:"spin_count"`
	SpinWindowMinutes int      `json:"spin_window_minutes"`
	EngineBusyPct     float64  `json:"engine_busy_pct"`
	BaselinePct       float64  `json:"baseline_pct"`
}

// ToEvidenceDTO decodes an insight_instances.evidence JSON blob (written
// by insight/engine.go's upsertFinding via plain json.Marshal of an
// insight.Evidence -- no tags, so its keys are the exact capitalized Go
// field names) into the wire DTO above. A decode failure -- corrupt
// data, which should never happen since the only writer is the engine's
// own json.Marshal of that exact type -- degrades to a zero-valued
// EvidenceDTO rather than an error: "Degradation, never errors" (plan
// Global Constraints) applies to a single instance's evidence exactly as
// much as to a missing collector.
func ToEvidenceDTO(raw string) EvidenceDTO {
	var ev insight.Evidence
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &ev)
	}
	return EvidenceDTO(ev)
}

// InsightDTO is the wire shape of one insight_instances row. Evidence is
// a pointer, omitted entirely (not just zero-valued) when the caller
// asks for the compact shape -- the frame's insights.active block never
// carries it (Task 9: "statements included, evidence excluded"), while
// GET /api/insights, GET /api/insights/history, GET /api/insights/{id},
// and the dismiss response all populate it. ToInsightDTO is exported
// (unlike alerts' per-call-site inline DTO construction) because BOTH
// this package's own handlers below AND main.go's buildInsightsBlock
// need the identical conversion for the identical struct -- alerts never
// needed to share one, since its frame DTO (FiringAlertDTO) is
// deliberately narrower than its REST one (AlertInstanceDTO).
type InsightDTO struct {
	ID            int64        `json:"id"`
	RuleID        string       `json:"rule_id"`
	VictimKind    string       `json:"victim_kind"`
	Victim        string       `json:"victim"`
	Culprit       string       `json:"culprit"`
	Culprits      string       `json:"culprits"`
	Resource      string       `json:"resource"`
	State         string       `json:"state"`
	Severity      string       `json:"severity"`
	Confidence    string       `json:"confidence"`
	Tier          string       `json:"tier"`
	Statement     string       `json:"statement"`
	StartedAt     int64        `json:"started_at"`
	FiredAt       int64        `json:"fired_at"`
	ResolvedAt    int64        `json:"resolved_at"`
	ResolveReason string       `json:"resolve_reason"`
	Evidence      *EvidenceDTO `json:"evidence,omitempty"`
}

// ToInsightDTO converts one store row, decoding its evidence blob only
// when includeEvidence is true -- see InsightDTO's own doc for why the
// frame block always passes false.
func ToInsightDTO(inst store.InsightInstance, includeEvidence bool) InsightDTO {
	dto := InsightDTO{
		ID: inst.ID, RuleID: inst.RuleID, VictimKind: inst.VictimKind, Victim: inst.Victim,
		Culprit: inst.Culprit, Culprits: inst.Culprits, Resource: inst.Resource, State: inst.State,
		Severity: inst.Severity, Confidence: inst.Confidence, Tier: inst.Tier, Statement: inst.Statement,
		StartedAt: inst.StartedAt, FiredAt: inst.FiredAt, ResolvedAt: inst.ResolvedAt, ResolveReason: inst.ResolveReason,
	}
	if includeEvidence {
		ev := ToEvidenceDTO(inst.Evidence)
		dto.Evidence = &ev
	}
	return dto
}

func toInsightDTOs(insts []store.InsightInstance, includeEvidence bool) []InsightDTO {
	out := make([]InsightDTO, len(insts))
	for i, inst := range insts {
		out[i] = ToInsightDTO(inst, includeEvidence)
	}
	return out
}

// InsightsBlockDTO serves two roles with the exact same shape: the SSE
// frame's insights block (SnapshotDTO.Insights, Active items carrying no
// evidence) and GET /api/insights' own response (Active items WITH
// evidence -- Task 9: "active findings (with evidence bundles)"). One
// struct for both, rather than a second envelope type, because the two
// truly are the same {active, tier, suppressed} shape and differ only in
// whether each Active item's own Evidence pointer is populated -- see
// ToInsightDTO's own doc for why that conversion is centralized instead
// of duplicated per call site.
type InsightsBlockDTO struct {
	Active     []InsightDTO `json:"active"`
	Tier       string       `json:"tier"`
	Suppressed int          `json:"suppressed"`
}

// InsightRuleDTO is one compiled-in rule's current tuning, for GET/PUT
// /api/insights/rules. Thresholds is the EFFECTIVE set (defaults merged
// with this rule's own overrides, insight.Rules' own merge); Defaults is
// the compiled-in set with no overrides applied, so the UI's "reset to
// default" control never has to hardcode librarySpecs itself -- the
// alerts rule editor's own ?defaults=1 idea, given a home directly on
// this DTO instead of a query param, since every rule (not just one
// requested set) always needs both at once here.
type InsightRuleDTO struct {
	RuleID     string             `json:"rule_id"`
	Title      string             `json:"title"`
	Tier       string             `json:"tier"`
	PSIUpgrade bool               `json:"psi_upgrade"`
	Enabled    bool               `json:"enabled"`
	Notify     bool               `json:"notify"`
	Thresholds map[string]float64 `json:"thresholds"`
	Defaults   map[string]float64 `json:"defaults"`
	UpdatedAt  int64              `json:"updated_at"`
}

type insightRulesResponse struct {
	Rules []InsightRuleDTO `json:"rules"`
}

// insightRuleInput is PUT /api/insights/rules' per-rule wire shape:
// enable/notify/overrides ONLY -- there is no id/title/tier/eval field
// to submit at all, so an attempt to change a rule's shape isn't merely
// rejected, it has nowhere on this type to even land; combined with
// DisallowUnknownFields below, any such attempt 400s at decode time
// (Task 9: "rule shape is not writable and an attempt is a 400").
type insightRuleInput struct {
	RuleID    string             `json:"rule_id"`
	Enabled   bool               `json:"enabled"`
	Notify    bool               `json:"notify"`
	Overrides map[string]float64 `json:"overrides"`
}

type insightRulesPutRequest struct {
	Rules []insightRuleInput `json:"rules"`
}

// insightDismissRequest is POST /api/insights/{id}/dismiss's body --
// Task 9's own {days:int} shape, the "1d/7d/30d" preset control Task
// 11's Active row renders.
type insightDismissRequest struct {
	Days int `json:"days"`
}

// --- decoded-overrides helpers ------------------------------------------

// decodeOverrides parses one insight_rule_config row's Overrides JSON
// blob (empty string means "no overrides", the exact convention
// insight/engine.go's own Tick reads) into a plain map, degrading to nil
// on a corrupt blob rather than erroring -- see ToEvidenceDTO's own doc
// for the identical "the only writer is this package itself" reasoning.
func decodeOverrides(raw string) map[string]float64 {
	if raw == "" {
		return nil
	}
	var ov map[string]float64
	_ = json.Unmarshal([]byte(raw), &ov)
	return ov
}

// encodeOverrides is decodeOverrides' inverse for the PUT path: a nil or
// empty map is stored as "" (never the literal string "null" a bare
// json.Marshal(map[string]float64(nil)) would produce), matching every
// seeded row's own convention (store.InsightRuleConfig zero value) so a
// rule that has never been tuned always reads back as "" either way it
// got there.
func encodeOverrides(m map[string]float64) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// buildInsightRuleDTOs zips the compiled-in library (insight.Rules,
// already merged against overridesByID) against insight.DefaultRules'
// own zero-override set (for Defaults) and the store's config rows (for
// enabled/notify/updated_at), producing GET /api/insights/rules' full
// list -- shared by the GET handler and the PUT handler's own
// respond-with-the-fresh-state tail, so the two can never drift.
func buildInsightRuleDTOs(cfgs []store.InsightRuleConfig) []InsightRuleDTO {
	cfgByID := make(map[string]store.InsightRuleConfig, len(cfgs))
	overridesByID := make(map[string]map[string]float64, len(cfgs))
	for _, c := range cfgs {
		cfgByID[c.RuleID] = c
		if ov := decodeOverrides(c.Overrides); ov != nil {
			overridesByID[c.RuleID] = ov
		}
	}

	effective := insight.Rules(overridesByID)
	defaults := insight.DefaultRules()
	defaultThresholds := make(map[string]map[string]float64, len(defaults))
	for _, r := range defaults {
		defaultThresholds[r.ID] = r.Thresholds
	}

	out := make([]InsightRuleDTO, len(effective))
	for i, rule := range effective {
		// A rule with no config row at all (shouldn't happen past boot --
		// main.go seeds every compiled-in id via SeedInsightRuleConfigs --
		// but a test double or a pre-seed race must still degrade sanely)
		// defaults to enabled/no-notify, insight.DefaultRuleConfigs' own
		// seed values, rather than the zero-valued store.InsightRuleConfig
		// Go would otherwise hand back (Enabled=false, silently hiding a
		// rule that should be on).
		cfg, ok := cfgByID[rule.ID]
		if !ok {
			cfg = store.InsightRuleConfig{RuleID: rule.ID, Enabled: true}
		}
		out[i] = InsightRuleDTO{
			RuleID: rule.ID, Title: rule.Title, Tier: rule.Tier.String(), PSIUpgrade: rule.PSIUpgrade,
			Enabled: cfg.Enabled, Notify: cfg.Notify, Thresholds: rule.Thresholds,
			Defaults: defaultThresholds[rule.ID], UpdatedAt: cfg.UpdatedAt,
		}
	}
	return out
}

// knownInsightRuleIDs is the compiled-in library's own id set, read
// straight off insight.DefaultRules() rather than hand-copied here --
// exactly the reasoning insight.DefaultRuleConfigs' own doc gives for
// not duplicating librarySpecs' seven strings a second time anywhere
// else in the codebase.
func knownInsightRuleIDs() map[string]bool {
	out := map[string]bool{}
	for _, r := range insight.DefaultRules() {
		out[r.ID] = true
	}
	return out
}

// --- GET /api/insights ---------------------------------------------------

// handleInsightsGet serves GET /api/insights: every active finding WITH
// its evidence bundle (Task 9), plus the live pressure tier and the
// engine's own suppressed-by-cap count. Options.Insights is nil in tests
// that don't wire one -- an empty active list at tier "proxy" is the
// harmless response, matching Alerts' own nil->empty convention.
func (s *Server) handleInsightsGet(w http.ResponseWriter, r *http.Request) {
	if s.opts.Insights == nil {
		writeJSON(w, InsightsBlockDTO{Active: []InsightDTO{}, Tier: "proxy"})
		return
	}
	active, err := s.opts.Insights.Active(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, InsightsBlockDTO{
		Active: toInsightDTOs(active, true), Tier: s.opts.Insights.Tier(), Suppressed: s.opts.Insights.Suppressed(),
	})
}

// --- GET /api/insights/{id} ----------------------------------------------

// handleInsightGet serves GET /api/insights/{id}: the evidence drawer's
// own fetch (Task 11), active or resolved either way -- a resolved
// finding's evidence is exactly as inspectable as an active one's (the
// bundle is denormalised at fire time and never changes again, plan
// Open question 4).
func (s *Server) handleInsightGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad insight id")
		return
	}
	if s.opts.Insights == nil {
		writeError(w, http.StatusNotFound, "insights unavailable")
		return
	}
	inst, ok, err := s.opts.Insights.ByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "insight not found")
		return
	}
	writeJSON(w, ToInsightDTO(inst, true))
}

// --- GET /api/insights/history --------------------------------------------

// handleInsightsHistory serves GET /api/insights/history?from&to&limit:
// resolved instances, newest resolution first, evidence included (a
// history row's numbers are exactly as inspectable as an active row's --
// see handleInsightGet's own doc). limit defaults to 100, capped at
// 500 -- the exact handleAlertsHistory precedent.
func (s *Server) handleInsightsHistory(w http.ResponseWriter, r *http.Request) {
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
	limit, ok := parseInt64Param(q, "limit", defaultInsightHistoryLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad limit")
		return
	}
	if limit <= 0 {
		limit = defaultInsightHistoryLimit
	}
	if limit > maxInsightHistoryLimit {
		limit = maxInsightHistoryLimit
	}

	if s.opts.Insights == nil {
		writeJSON(w, []InsightDTO{})
		return
	}
	history, err := s.opts.Insights.History(r.Context(), from, to, int(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, toInsightDTOs(history, true))
}

// --- GET/PUT /api/insights/rules -----------------------------------------

// handleInsightsRulesGet serves GET /api/insights/rules. Options.
// Insights is nil in tests that don't wire one -- an empty list, matching
// Alerts' own nil->empty convention (unlike alerts' own ?defaults=1
// branch, there's no store-free path here: Defaults is already a field
// on every row, not a separate query mode, so nil options simply means
// nothing to report at all).
func (s *Server) handleInsightsRulesGet(w http.ResponseWriter, r *http.Request) {
	if s.opts.Insights == nil {
		writeJSON(w, insightRulesResponse{Rules: []InsightRuleDTO{}})
		return
	}
	cfgs, err := s.opts.Insights.RuleConfigs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, insightRulesResponse{Rules: buildInsightRuleDTOs(cfgs)})
}

// handleInsightsRulesPut serves PUT /api/insights/rules: enable/notify/
// overrides only, one SaveRuleConfig call per submitted rule -- there is
// no "replace the rest wholesale" second half the way alerts' non-
// builtin rules need, because every insight rule is always compiled-in
// (insight.DefaultRuleConfigs' own doc); an omitted rule id is simply
// left exactly as it was, never treated as a deletion attempt.
//
// insightRuleInput's own shape already makes "change a rule's metric or
// shape" impossible to express (no such field exists to decode into),
// and DisallowUnknownFields turns any attempt to smuggle one in as an
// extra JSON key into a 400 at the door -- see that type's own doc.
// The remaining validation here is identity: every rule_id must name a
// real compiled-in rule (rejecting an invented one a client can't
// actually make the engine evaluate) and no id may repeat.
func (s *Server) handleInsightsRulesPut(w http.ResponseWriter, r *http.Request) {
	if s.opts.Insights == nil {
		writeError(w, http.StatusNotFound, "insights unavailable")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body insightRulesPutRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(body.Rules) > maxInsightRulesSubmission {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many rules: %d exceeds the cap of %d", len(body.Rules), maxInsightRulesSubmission))
		return
	}

	known := knownInsightRuleIDs()
	seen := map[string]bool{}
	now := time.Now().Unix()
	for _, in := range body.Rules {
		if !known[in.RuleID] {
			writeError(w, http.StatusBadRequest, "unknown insight rule "+in.RuleID)
			return
		}
		if seen[in.RuleID] {
			writeError(w, http.StatusBadRequest, "duplicate rule id "+in.RuleID)
			return
		}
		seen[in.RuleID] = true

		if err := s.opts.Insights.SaveRuleConfig(store.InsightRuleConfig{
			RuleID: in.RuleID, Enabled: in.Enabled, Notify: in.Notify,
			Overrides: encodeOverrides(in.Overrides), UpdatedAt: now,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	cfgs, err := s.opts.Insights.RuleConfigs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, insightRulesResponse{Rules: buildInsightRuleDTOs(cfgs)})
}

// --- POST /api/insights/{id}/dismiss -------------------------------------

// handleInsightDismiss serves POST /api/insights/{id}/dismiss: records a
// dismissal for the target instance's own (rule, victim, culprit,
// resource) identity -- read straight off the instance itself, never
// from the request body, so a client can never dismiss a tuple it
// doesn't actually name a real instance for -- then resolves it with
// reason "dismissed" (Task 9). days must be 1-30 (the "1d/7d/30d" preset
// control's own range, tolerant of any value in it).
//
// dismissalMatches' own all-four-empty fail-safe (insight/engine.go) can
// never actually trigger from this path: Resource is NOT NULL in
// 004_insights.sql and every rule's Eval always populates it (there is
// no finding shape that leaves it ""), so a dismissal built from a real
// instance's own columns can never be the all-empty row that doc warns
// about -- unlike alerts' silences, which accept a raw client-submitted
// rule_id/entity and so DO need an explicit "scope":"all" gesture to
// permit that case at all.
func (s *Server) handleInsightDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad insight id")
		return
	}
	if s.opts.Insights == nil {
		writeError(w, http.StatusNotFound, "insights unavailable")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body insightDismissRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.Days < minInsightDismissDays || body.Days > maxInsightDismissDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("days must be between %d and %d", minInsightDismissDays, maxInsightDismissDays))
		return
	}

	inst, ok, err := s.opts.Insights.ByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "insight not found")
		return
	}

	now := time.Now().Unix()
	if _, err := s.opts.Insights.AddDismissal(store.InsightDismissal{
		RuleID: inst.RuleID, Victim: inst.Victim, Culprit: inst.Culprit, Resource: inst.Resource,
		Until: now + int64(body.Days)*86400, CreatedAt: now,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.opts.Insights.Resolve(id, now, "dismissed"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	inst, _, err = s.opts.Insights.ByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, ToInsightDTO(inst, true))
}

// --- GET /api/insights/graph ----------------------------------------------

// resourceNodePrefix namespaces a contended resource's graph node id
// away from any container name -- container names and this prefix can
// never collide (Docker's own naming rule disallows ':' entirely), so a
// container literally named e.g. "cpu" can never be confused with the
// "cpu" resource node.
const resourceNodePrefix = "resource:"

// GraphNodeDTO is one node in GET /api/insights/graph's payload: either
// a container or a contended resource (a disk slot, "cpu", "memory", or
// "gpu:<engine>" -- insight_instances.resource's own vocabulary,
// 004_insights.sql). Column placement (culprit | resource | victim) is
// deliberately NOT decided here -- that's mapLayout.ts's own pure,
// client-side job (Task 14); this endpoint hands over the raw graph.
type GraphNodeDTO struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "container" | "resource"
	Label string `json:"label"`
}

// GraphEdgeDTO is one leg of one active insight's causal claim.
//
// Every insight contributes a CULPRIT edge (culprit container -> the
// contended resource) and, ONLY when the finding names a specific
// VICTIM CONTAINER, a VICTIM edge (the resource -> that container).
// This hub-and-spoke shape -- never a direct culprit-to-victim edge that
// skips the resource -- is what makes the resource column a real graph
// participant rather than a label floating near a curve, and it is what
// lets a host/array-wide finding (no victim container at all -- e.g.
// io-driven-cpu-load's victim is the host CPU itself) still terminate
// somewhere meaningful: at the resource node. Every edge therefore
// always has exactly two endpoints, satisfying the plan's own hover
// contract ("highlights its two endpoints") uniformly for every shape.
//
// "Names a specific victim container" is VictimKind == "container" AND
// Victim != "" -- NOT just Victim != "", because
// gpu-engine-contention's own Finding sets VictimKind "gpu" with Victim
// holding the bare engine name (e.g. "video", Resource "gpu:video") --
// the engine identity, not a container. Treating that as a container
// node would invent a container that doesn't exist and draw a
// nonsensical gpu:video -> video edge; every other non-container
// VictimKind (host/array/disk) already leaves Victim "" (see e.g.
// evalDiskSpinupChurn/evalParitySlowdown), so the kind check is the one
// gate that also happens to cover gpu's own exception correctly.
//
// A Shared culprit set fans in: one culprit edge per named culprit, all
// pointing at the same resource node, each carrying the SAME combined
// SharePct -- insight.Evidence.CulpritSharePct is already the set's
// combined fraction, not a per-name split, so there is no per-name
// number to give each fan-in edge a distinct width.
type GraphEdgeDTO struct {
	ID         string  `json:"id"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Kind       string  `json:"kind"` // "culprit" | "victim"
	InsightID  int64   `json:"insight_id"`
	RuleID     string  `json:"rule_id"`
	Confidence string  `json:"confidence"`
	Severity   string  `json:"severity"`
	SharePct   float64 `json:"share_pct"`
}

type InsightGraphDTO struct {
	Nodes []GraphNodeDTO `json:"nodes"`
	Edges []GraphEdgeDTO `json:"edges"`
}

// culpritNames splits an instance's culprit identity into a plain name
// list regardless of shape: a single-culprit row's Culprit alone, or a
// Shared row's comma-joined Culprits (culpritColumns' own encoding,
// insight/engine.go) -- so buildInsightGraph never special-cases either
// shape past this one call.
func culpritNames(inst store.InsightInstance) []string {
	if inst.Culprit != "" {
		return []string{inst.Culprit}
	}
	if inst.Culprits == "" {
		return nil
	}
	return strings.Split(inst.Culprits, ",")
}

// buildInsightGraph derives GET /api/insights/graph's payload from the
// current active set -- see GraphEdgeDTO's own doc for the hub-and-spoke
// edge shape this builds. Nodes and edges are both sorted by id before
// returning: Go map iteration order is otherwise random, and a graph
// endpoint whose node/edge ORDER jitters between two otherwise-identical
// polls would be a needless source of visual churn on a view whose
// entire design point (mapLayout.ts's own doc, Task 14) is that node
// position is stable identity.
func buildInsightGraph(active []store.InsightInstance) InsightGraphDTO {
	nodes := map[string]GraphNodeDTO{}
	edges := make([]GraphEdgeDTO, 0, len(active)*2)

	ensureNode := func(id, kind, label string) {
		if _, ok := nodes[id]; !ok {
			nodes[id] = GraphNodeDTO{ID: id, Kind: kind, Label: label}
		}
	}

	for _, inst := range active {
		resID := resourceNodePrefix + inst.Resource
		ensureNode(resID, "resource", inst.Resource)

		ev := ToEvidenceDTO(inst.Evidence)
		for i, name := range culpritNames(inst) {
			ensureNode(name, "container", name)
			edges = append(edges, GraphEdgeDTO{
				ID: fmt.Sprintf("%d:culprit:%d", inst.ID, i), From: name, To: resID, Kind: "culprit",
				InsightID: inst.ID, RuleID: inst.RuleID, Confidence: inst.Confidence, Severity: inst.Severity,
				SharePct: ev.CulpritSharePct,
			})
		}

		if inst.VictimKind == "container" && inst.Victim != "" {
			ensureNode(inst.Victim, "container", inst.Victim)
			edges = append(edges, GraphEdgeDTO{
				ID: fmt.Sprintf("%d:victim", inst.ID), From: resID, To: inst.Victim, Kind: "victim",
				InsightID: inst.ID, RuleID: inst.RuleID, Confidence: inst.Confidence, Severity: inst.Severity,
				SharePct: ev.VictimStallPct,
			})
		}
	}

	out := InsightGraphDTO{Nodes: make([]GraphNodeDTO, 0, len(nodes)), Edges: edges}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	sort.Slice(out.Edges, func(i, j int) bool { return out.Edges[i].ID < out.Edges[j].ID })
	return out
}

// handleInsightsGraph serves GET /api/insights/graph. Options.Insights
// is nil in tests that don't wire one -- an empty graph, matching every
// other GET route's nil->empty convention (the D2-calm empty state IS
// the correct rendering of an empty graph, not a special case the UI
// needs to detect separately).
func (s *Server) handleInsightsGraph(w http.ResponseWriter, r *http.Request) {
	if s.opts.Insights == nil {
		writeJSON(w, InsightGraphDTO{Nodes: []GraphNodeDTO{}, Edges: []GraphEdgeDTO{}})
		return
	}
	active, err := s.opts.Insights.Active(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, buildInsightGraph(active))
}
