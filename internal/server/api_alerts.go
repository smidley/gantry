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

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/store"
)

const (
	// maxAlertRules caps PUT /api/alerts/rules' whole-document size --
	// the same defensive cap alert.maxWebhookTargets applies to webhook
	// targets, here so a hand-rolled or buggy client can't grow
	// alert_rules without bound.
	maxAlertRules = 100

	defaultAlertHistoryLimit = 100
	maxAlertHistoryLimit     = 500

	minSilenceHours = 1
	maxSilenceHours = 720 // 30 days
)

// --- AlertsIface / WebhooksIface -----------------------------------------

// AlertsIface is the minimal store+dispatch surface /api/alerts, /api/
// alerts/rules, /api/alerts/history, and /api/alerts/silences need --
// main wires a small adapter over *store.Store plus the running *alert.
// Dispatcher's own Channels field for health, keeping this package
// store/alert-shape-agnostic the same way Query/Top/Events/Settings
// already do (see Options.Settings' own doc). Nil in tests that don't
// wire one: every GET route reports its own meaningful empty (see each
// handler's doc); every write route 404s, matching Settings' PUT.
type AlertsIface interface {
	// Active returns every instance with resolved_at = 0 (store.
	// ActiveAlertInstances: both "pending" and "firing") for GET
	// /api/alerts -- the handler itself filters to "firing" before
	// writing the response; a pending alert is engine bookkeeping, not
	// a user-facing state (matching the frame block's own rule).
	Active(ctx context.Context) ([]store.AlertInstance, error)
	// History returns resolved instances in [from,to] (either 0 = no
	// bound), newest resolution first, capped at limit, for GET
	// /api/alerts/history.
	History(ctx context.Context, from, to int64, limit int) ([]store.AlertInstance, error)
	// Rules returns every configured rule, builtin and user, for GET
	// /api/alerts/rules.
	Rules(ctx context.Context) ([]store.AlertRule, error)
	// UpsertRule edits one existing builtin rule's numbers in place --
	// the only path a builtin ever changes through (store.
	// UpsertAlertRule is ReplaceRules' counterpart for a row ReplaceRules
	// itself always skips; see ReplaceRules' own doc below).
	UpsertRule(r store.AlertRule) error
	// ReplaceRules performs the store.ReplaceAlertRules-shaped whole-
	// document write for every USER rule; a builtin-flagged row in
	// rules is silently skipped by the store itself, so
	// handleAlertsRulesPut calls UpsertRule for builtins separately and
	// hands this the complete submitted list.
	ReplaceRules(rules []store.AlertRule) error
	// Silences returns every silence not yet expired (store.Silences,
	// with "now" resolved by the adapter), for GET /api/alerts.
	Silences(ctx context.Context) ([]store.Silence, error)
	// AddSilence inserts a new silence for POST /api/alerts/silences,
	// returning it with its generated ID.
	AddSilence(sil store.Silence) (store.Silence, error)
	// DeleteSilence lifts a silence for DELETE /api/alerts/silences/{id}
	// -- a no-op, not an error, for an id that's already gone (store.
	// DeleteSilence's own doc).
	DeleteSilence(id int64) error
	// Channels reports every configured delivery channel's current
	// health ("ok" or its enable hint/failure text), keyed by Channel.
	// ID() -- main wires this over the running *alert.Dispatcher's own
	// Channels field, the same data the frame's alerts.channels surfaces.
	Channels() map[string]string
}

// WebhooksIface is the minimal surface PUT/GET /api/alerts/webhooks
// needs -- main wires a small adapter over the settings-blob-backed
// target list Task 7 already built (loadWebhookTargets/
// saveWebhookTargets) plus whether GANTRY_WEBHOOK_URL is currently set.
// Nil in tests that don't wire one -- GET reports an empty target list
// (meaningful empty, matching Settings' own convention), PUT 404s.
type WebhooksIface interface {
	// Targets returns the current configured list and whether
	// GANTRY_WEBHOOK_URL is set. When true, the "env" target's URL/
	// Enabled/TimeoutS are locked to whatever seedWebhookTargetFromEnv
	// last wrote at boot, mirroring SettingsIface.Get's env-overridden
	// signal.
	Targets() (targets []alert.WebhookTarget, envLocked bool, err error)
	// Replace performs the whole-document write for PUT /api/alerts/
	// webhooks. The caller (handleAlertsWebhooksPut) has already run
	// alert.ValidateWebhookTargets and the env-lock conflict check;
	// Replace only persists.
	Replace(targets []alert.WebhookTarget) error
}

// --- wire DTOs -------------------------------------------------------------

// AlertRuleDTO is the wire shape of one alert_rules row. Field names,
// types, and order are IDENTICAL to store.AlertRule, on purpose:
// converting between them is a plain type conversion (AlertRuleDTO(r) /
// store.AlertRule(dto)), the same "identical fields, ignoring json tags"
// idiom api_history.go's topRowDTO already uses -- a future field added
// to one side without the other fails to COMPILE, not silently drops
// data.
type AlertRuleDTO struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Enabled           bool    `json:"enabled"`
	Builtin           bool    `json:"builtin"`
	Type              string  `json:"type"`
	Kind              string  `json:"kind"`
	EntityGlob        string  `json:"entity_glob"`
	EntityClass       string  `json:"entity_class"`
	Metric            string  `json:"metric"`
	Op                string  `json:"op"`
	Threshold         float64 `json:"threshold"`
	ClearThreshold    float64 `json:"clear_threshold"`
	WarnThreshold     float64 `json:"warn_threshold"`
	CriticalThreshold float64 `json:"critical_threshold"`
	BandFamily        string  `json:"band_family"`
	ForSeconds        int64   `json:"for_seconds"`
	ClearSeconds      int64   `json:"clear_seconds"`
	EventKinds        string  `json:"event_kinds"`
	MinSeverity       string  `json:"min_severity"`
	ClearEventKinds   string  `json:"clear_event_kinds"`
	ClearMaxSeverity  string  `json:"clear_max_severity"`
	Severity          string  `json:"severity"`
	Channels          string  `json:"channels"`
	RenotifyHours     int64   `json:"renotify_hours"`
	UpdatedAt         int64   `json:"updated_at"`
}

func toAlertRuleDTOs(rules []store.AlertRule) []AlertRuleDTO {
	out := make([]AlertRuleDTO, len(rules))
	for i, r := range rules {
		out[i] = AlertRuleDTO(r)
	}
	return out
}

// AlertInstanceDTO is the wire shape of one alert_instances row, plus
// Silenced -- a field store.AlertInstance itself has no column for; it's
// computed fresh from the current silence list at response time (see
// toAlertInstanceDTO).
type AlertInstanceDTO struct {
	ID             int64   `json:"id"`
	RuleID         string  `json:"rule_id"`
	Kind           string  `json:"kind"`
	Entity         string  `json:"entity"`
	Metric         string  `json:"metric"`
	State          string  `json:"state"`
	Severity       string  `json:"severity"`
	Value          float64 `json:"value"`
	Threshold      float64 `json:"threshold"`
	Summary        string  `json:"summary"`
	StartedAt      int64   `json:"started_at"`
	FiredAt        int64   `json:"fired_at"`
	ResolvedAt     int64   `json:"resolved_at"`
	ResolveReason  string  `json:"resolve_reason"`
	LastNotifiedAt int64   `json:"last_notified_at"`
	NotifyCount    int64   `json:"notify_count"`
	Silenced       bool    `json:"silenced"`
}

func toAlertInstanceDTO(i store.AlertInstance, silenced bool) AlertInstanceDTO {
	return AlertInstanceDTO{
		ID: i.ID, RuleID: i.RuleID, Kind: i.Kind, Entity: i.Entity, Metric: i.Metric,
		State: i.State, Severity: i.Severity, Value: i.Value, Threshold: i.Threshold,
		Summary: i.Summary, StartedAt: i.StartedAt, FiredAt: i.FiredAt, ResolvedAt: i.ResolvedAt,
		ResolveReason: i.ResolveReason, LastNotifiedAt: i.LastNotifiedAt, NotifyCount: i.NotifyCount,
		Silenced: silenced,
	}
}

// SilenceDTO mirrors store.Silence exactly (see AlertRuleDTO's own doc
// on the direct-conversion idiom this enables).
type SilenceDTO struct {
	ID        int64  `json:"id"`
	RuleID    string `json:"rule_id"`
	Entity    string `json:"entity"`
	Reason    string `json:"reason"`
	Until     int64  `json:"until"`
	CreatedAt int64  `json:"created_at"`
}

func toSilenceDTOs(silences []store.Silence) []SilenceDTO {
	out := make([]SilenceDTO, len(silences))
	for i, sil := range silences {
		out[i] = SilenceDTO(sil)
	}
	return out
}

// SilenceCovers reports whether any silence in the slice covers
// (ruleID, entity): "" on either field means "any". Mirrors alert/
// engine.go's own unexported silenced() helper (identical semantics --
// that one decides whether the engine actually dispatches a
// notification; this one only decides whether a wire response should
// flag a row as dimmed). Exported so main.go's snapshot assembly (the
// SSE frame's alerts block) can share this one copy instead of a third
// private one.
func SilenceCovers(silences []store.Silence, ruleID, entity string) bool {
	for _, s := range silences {
		if (s.RuleID == "" || s.RuleID == ruleID) && (s.Entity == "" || s.Entity == entity) {
			return true
		}
	}
	return false
}

// WebhookTargetDTO is GET /api/alerts/webhooks' per-target wire shape.
// HeaderValue never appears here -- HeaderSet stands in for it, the only
// way to show a secret is configured without ever echoing it back (plan
// Open question 1 / Task 7's own doc on WebhookTarget.HeaderValue).
type WebhookTargetDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Enabled       bool   `json:"enabled"`
	HeaderName    string `json:"header_name,omitempty"`
	HeaderSet     bool   `json:"header_set"`
	TimeoutS      int    `json:"timeout_s"`
	EnvOverridden bool   `json:"env_overridden,omitempty"`
}

func toWebhookTargetDTO(t alert.WebhookTarget, envOverridden bool) WebhookTargetDTO {
	return WebhookTargetDTO{
		ID: t.ID, Name: t.Name, URL: t.URL, Enabled: t.Enabled,
		HeaderName: t.HeaderName, HeaderSet: t.HeaderValue != "",
		TimeoutS: t.TimeoutS, EnvOverridden: envOverridden,
	}
}

func toWebhookTargetDTOs(targets []alert.WebhookTarget, envLocked bool) []WebhookTargetDTO {
	out := make([]WebhookTargetDTO, len(targets))
	for i, t := range targets {
		out[i] = toWebhookTargetDTO(t, envLocked && t.ID == "env")
	}
	return out
}

// webhookTargetInput is PUT /api/alerts/webhooks' per-target wire shape.
// HeaderValue is a *string, not string, so the decoder can tell
// "omitted" (nil: keep whatever's already stored for this id) from
// "submitted as empty" (non-nil pointing at ""), which explicitly clears
// it -- the only way to edit an existing secret without ever echoing it
// back first (see WebhookTargetDTO.HeaderSet).
type webhookTargetInput struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	Enabled     bool    `json:"enabled"`
	HeaderName  string  `json:"header_name"`
	HeaderValue *string `json:"header_value"`
	TimeoutS    int     `json:"timeout_s"`
}

// --- envelopes ---------------------------------------------------------

type alertsGetResponse struct {
	Active   []AlertInstanceDTO `json:"active"`
	Silences []SilenceDTO       `json:"silences"`
	Channels map[string]string  `json:"channels"`
}

type alertRulesResponse struct {
	Rules []AlertRuleDTO `json:"rules"`
}

// alertRulesPutRequest is decoded with DisallowUnknownFields, the exact
// /api/groups/-precedent whole-document envelope (see
// handleAlertsRulesPut's own doc).
type alertRulesPutRequest struct {
	Rules []AlertRuleDTO `json:"rules"`
}

type silenceCreateRequest struct {
	RuleID string `json:"rule_id"`
	Entity string `json:"entity"`
	Hours  int    `json:"hours"`
	Reason string `json:"reason"`
}

type webhooksGetResponse struct {
	Targets []WebhookTargetDTO `json:"targets"`
}

type webhooksPutRequest struct {
	Targets []webhookTargetInput `json:"targets"`
}

// --- GET /api/alerts -----------------------------------------------------

// handleAlertsGet serves GET /api/alerts: every FIRING instance (pending
// excluded -- engine bookkeeping, not a user-facing state, the same rule
// the frame's alerts block follows), every live silence, and every
// configured channel's health. Options.Alerts is nil in tests that don't
// wire one -- an empty active/silences list and an empty channels map is
// the harmless response, matching Containers'/Images' own nil->empty
// convention.
func (s *Server) handleAlertsGet(w http.ResponseWriter, r *http.Request) {
	if s.opts.Alerts == nil {
		writeJSON(w, alertsGetResponse{Active: []AlertInstanceDTO{}, Silences: []SilenceDTO{}, Channels: map[string]string{}})
		return
	}
	active, err := s.opts.Alerts.Active(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	silences, err := s.opts.Alerts.Silences(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	channels := s.opts.Alerts.Channels()
	if channels == nil {
		channels = map[string]string{}
	}

	firing := make([]store.AlertInstance, 0, len(active))
	for _, inst := range active {
		if inst.State == "firing" {
			firing = append(firing, inst)
		}
	}
	activeDTOs := make([]AlertInstanceDTO, len(firing))
	for i, inst := range firing {
		activeDTOs[i] = toAlertInstanceDTO(inst, SilenceCovers(silences, inst.RuleID, inst.Entity))
	}

	writeJSON(w, alertsGetResponse{Active: activeDTOs, Silences: toSilenceDTOs(silences), Channels: channels})
}

// --- GET/PUT /api/alerts/rules -------------------------------------------

// handleAlertsRulesGet serves GET /api/alerts/rules and, with
// ?defaults=1, the compiled-in seed set instead -- Task 11's per-builtin
// "reset to default" control reads this so the UI never hardcodes the
// Task 5 defaults table itself. The defaults branch needs no store at
// all (store.DefaultAlertRules is a pure, compiled-in list) so it
// answers even when Options.Alerts is nil.
func (s *Server) handleAlertsRulesGet(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("defaults") == "1" {
		writeJSON(w, alertRulesResponse{Rules: toAlertRuleDTOs(store.DefaultAlertRules())})
		return
	}
	if s.opts.Alerts == nil {
		writeJSON(w, alertRulesResponse{Rules: []AlertRuleDTO{}})
		return
	}
	rules, err := s.opts.Alerts.Rules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, alertRulesResponse{Rules: toAlertRuleDTOs(rules)})
}

// handleAlertsRulesPut serves PUT /api/alerts/rules: the /api/groups
// whole-document-replace contract (DisallowUnknownFields, no
// X-Gantry-Confirm, not READ_ONLY-gated -- config, not a destructive
// docker mutation; see Options.ReadOnly's own doc and the plan's Global
// Constraints). The submitted list is expected to carry every rule,
// builtin included, since the UI always PUTs back its own already-
// edited full GET -- but a submitted builtin=true row is applied
// through UpsertRule, never ReplaceRules, matching store.
// ReplaceAlertRules' own doc: it silently skips any builtin-flagged row
// rather than inserting or overwriting it.
//
// Three builtin-identity shapes are rejected 400 before anything is
// written: an existing builtin id missing from the submitted list
// entirely (deletion-by-omission -- builtins are disable-only, never
// deletable); an existing builtin id resubmitted with builtin=false
// (identity tampering -- ReplaceRules' plain INSERT would otherwise
// collide with alert_rules' PRIMARY KEY and surface as an opaque 500,
// the store review's carry-forward this fixes at the door instead); and
// a submitted builtin=true row whose id isn't a builtin the store
// actually knows about (a client can't invent a new one).
func (s *Server) handleAlertsRulesPut(w http.ResponseWriter, r *http.Request) {
	if s.opts.Alerts == nil {
		writeError(w, http.StatusNotFound, "alerts unavailable")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body alertRulesPutRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(body.Rules) > maxAlertRules {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many rules: %d exceeds the cap of %d", len(body.Rules), maxAlertRules))
		return
	}

	existing, err := s.opts.Alerts.Rules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	existingBuiltins := map[string]bool{}
	for _, er := range existing {
		if er.Builtin {
			existingBuiltins[er.ID] = true
		}
	}

	now := time.Now().Unix()
	submitted := make([]store.AlertRule, len(body.Rules))
	seenIDs := map[string]bool{}
	seenBuiltins := map[string]bool{}
	for i, dto := range body.Rules {
		rule := store.AlertRule(dto)
		rule.UpdatedAt = now // server-assigned, never trusted from the client

		if seenIDs[rule.ID] {
			writeError(w, http.StatusBadRequest, "duplicate rule id "+rule.ID)
			return
		}
		seenIDs[rule.ID] = true

		switch {
		case existingBuiltins[rule.ID] && !rule.Builtin:
			writeError(w, http.StatusBadRequest, "rule "+rule.ID+" is a builtin and cannot be redefined as a user rule")
			return
		case existingBuiltins[rule.ID]:
			seenBuiltins[rule.ID] = true
		case rule.Builtin:
			writeError(w, http.StatusBadRequest, "rule "+rule.ID+" is not a known builtin rule")
			return
		}

		if err := alert.ValidateRule(rule); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		submitted[i] = rule
	}

	var missing []string
	for id := range existingBuiltins {
		if !seenBuiltins[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		writeError(w, http.StatusBadRequest, "builtin rules cannot be removed: "+strings.Join(missing, ", "))
		return
	}

	for _, rule := range submitted {
		if rule.Builtin {
			if err := s.opts.Alerts.UpsertRule(rule); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	if err := s.opts.Alerts.ReplaceRules(submitted); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rules, err := s.opts.Alerts.Rules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, alertRulesResponse{Rules: toAlertRuleDTOs(rules)})
}

// --- GET /api/alerts/history -----------------------------------------------

// handleAlertsHistory serves GET /api/alerts/history?from=&to=&limit=:
// resolved instances, newest resolution first. limit defaults to 100,
// capped at 500 regardless of what's requested -- the same convention
// handleEvents already uses for defaultEventsLimit/maxEventsLimit.
// Silenced is always false on a history row: "currently silenced" isn't
// a meaningful question for an instance that's already resolved.
func (s *Server) handleAlertsHistory(w http.ResponseWriter, r *http.Request) {
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
	limit, ok := parseInt64Param(q, "limit", defaultAlertHistoryLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad limit")
		return
	}
	if limit <= 0 {
		limit = defaultAlertHistoryLimit
	}
	if limit > maxAlertHistoryLimit {
		limit = maxAlertHistoryLimit
	}

	if s.opts.Alerts == nil {
		writeJSON(w, []AlertInstanceDTO{})
		return
	}
	history, err := s.opts.Alerts.History(r.Context(), from, to, int(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]AlertInstanceDTO, len(history))
	for i, inst := range history {
		out[i] = toAlertInstanceDTO(inst, false)
	}
	writeJSON(w, out)
}

// --- silences --------------------------------------------------------------

// handleAlertsSilencesPost serves POST /api/alerts/silences: snooze one
// rule/entity pair (or every entity of a rule, or every rule on one
// entity, whichever side is left "") for hours (1-720, i.e. up to 30
// days). Not READ_ONLY-gated and no X-Gantry-Confirm -- config-shaped,
// the same /api/settings precedent PUT /api/alerts/rules follows (plan
// Global Constraints).
func (s *Server) handleAlertsSilencesPost(w http.ResponseWriter, r *http.Request) {
	if s.opts.Alerts == nil {
		writeError(w, http.StatusNotFound, "alerts unavailable")
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body silenceCreateRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.Hours < minSilenceHours || body.Hours > maxSilenceHours {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("hours must be between %d and %d", minSilenceHours, maxSilenceHours))
		return
	}

	now := time.Now().Unix()
	created, err := s.opts.Alerts.AddSilence(store.Silence{
		RuleID: body.RuleID, Entity: body.Entity, Reason: body.Reason,
		Until: now + int64(body.Hours)*3600, CreatedAt: now,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, SilenceDTO(created))
}

// handleAlertsSilencesDelete serves DELETE /api/alerts/silences/{id}:
// 204 whether or not id existed (store.DeleteSilence's own "already
// lifted or pruned" no-op convention) -- lifting a silence is naturally
// idempotent from the caller's point of view.
func (s *Server) handleAlertsSilencesDelete(w http.ResponseWriter, r *http.Request) {
	if s.opts.Alerts == nil {
		writeError(w, http.StatusNotFound, "alerts unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad silence id")
		return
	}
	if err := s.opts.Alerts.DeleteSilence(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- webhooks --------------------------------------------------------------

// handleAlertsWebhooksGet serves GET /api/alerts/webhooks. HeaderValue
// never appears in this response -- see WebhookTargetDTO's own doc.
// Options.Webhooks is nil in tests that don't wire one -- an empty
// target list is the harmless response, matching Settings'/Images' own
// nil->empty convention.
func (s *Server) handleAlertsWebhooksGet(w http.ResponseWriter, r *http.Request) {
	if s.opts.Webhooks == nil {
		writeJSON(w, webhooksGetResponse{Targets: []WebhookTargetDTO{}})
		return
	}
	targets, envLocked, err := s.opts.Webhooks.Targets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, webhooksGetResponse{Targets: toWebhookTargetDTOs(targets, envLocked)})
}

// handleAlertsWebhooksPut serves PUT /api/alerts/webhooks: whole-
// document replace, 403 under GANTRY_READ_ONLY=1 (webhook-target writes
// configure an outbound side-effect capability -- the one write path
// this plan gates by READ_ONLY at all; rules/silences don't, see
// handleAlertsRulesPut's own doc for the asymmetry). header_value is
// write-only and optional per target: omitted keeps whatever's already
// stored for that id, an explicit "" clears it (see webhookTargetInput's
// own doc).
//
// The "env" target (present only while GANTRY_WEBHOOK_URL is set) is
// read-only for URL/Enabled/TimeoutS, mirroring /api/settings' per-field
// env-lock: a submission that drops it entirely or changes one of those
// three fields 409s naming it; one that resubmits it unchanged for
// those three (editing only its header, say) is accepted -- the same
// "differing write 409s, identical write no-ops" contract Settings' PUT
// documents.
func (s *Server) handleAlertsWebhooksPut(w http.ResponseWriter, r *http.Request) {
	if s.opts.Webhooks == nil {
		writeError(w, http.StatusNotFound, "webhooks unavailable")
		return
	}
	if s.opts.ReadOnly {
		writeError(w, http.StatusForbidden, "read-only mode")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body webhooksPutRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	current, envLocked, err := s.opts.Webhooks.Targets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	currentByID := make(map[string]alert.WebhookTarget, len(current))
	for _, t := range current {
		currentByID[t.ID] = t
	}

	resolved := make([]alert.WebhookTarget, len(body.Targets))
	envSubmitted := false
	for i, in := range body.Targets {
		t := alert.WebhookTarget{
			ID: in.ID, Name: in.Name, URL: in.URL, Enabled: in.Enabled,
			HeaderName: in.HeaderName, TimeoutS: in.TimeoutS,
		}
		if in.HeaderValue != nil {
			t.HeaderValue = *in.HeaderValue
		} else {
			t.HeaderValue = currentByID[in.ID].HeaderValue
		}

		if t.ID == "env" {
			envSubmitted = true
			if want, ok := currentByID["env"]; envLocked && ok {
				if t.URL != want.URL || t.Enabled != want.Enabled || t.TimeoutS != want.TimeoutS {
					writeError(w, http.StatusConflict, `webhook target "env" is set by GANTRY_WEBHOOK_URL and cannot be changed here`)
					return
				}
			}
		}
		resolved[i] = t
	}
	if _, hadEnv := currentByID["env"]; envLocked && hadEnv && !envSubmitted {
		writeError(w, http.StatusConflict, `webhook target "env" is set by GANTRY_WEBHOOK_URL and cannot be removed here`)
		return
	}

	if err := alert.ValidateWebhookTargets(resolved); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.opts.Webhooks.Replace(resolved); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	targets, envLocked2, err := s.opts.Webhooks.Targets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, webhooksGetResponse{Targets: toWebhookTargetDTOs(targets, envLocked2)})
}
