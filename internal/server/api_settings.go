package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// RetentionSettings is the wire shape of /api/settings' "retention"
// object -- the four fields Task 10 makes editable, named to match their
// store/config keys (retention.<name>) with the dot dropped.
type RetentionSettings struct {
	R1Hours   int `json:"r1_hours"`
	R2Days    int `json:"r2_days"`
	R3Days    int `json:"r3_days"`
	SizeCapMB int `json:"size_cap_mb"`
}

// SettingsIface is the minimal store+config surface /api/settings needs
// (main wires a small adapter over *config.Config for Get and
// *store.Store for Set), keeping this package store/config-shape-
// agnostic the same way Query/Top/Events do.
type SettingsIface interface {
	// Get returns the four retention keys' current effective values
	// plus which of those four (by their wire name, e.g. "r1_hours")
	// currently have their GANTRY_* env var set. Env always overrides a
	// stored setting (see internal/config), so PUT refuses to write an
	// overridden field at all rather than silently no-op it.
	Get() (RetentionSettings, map[string]bool)
	// Set persists one already-validated field (by its wire name) to
	// the settings store. Effective on the next maintenance tick, not
	// immediately -- see cmd/gantry/main.go's per-tick
	// RetentionFromConfig resolve, which is what makes that true.
	Set(field string, value int) error
}

// retentionField describes one whitelisted /api/settings field: its wire
// name (RetentionSettings' json tag) and inclusive valid range. Order
// here is also the order fields appear in env_overridden and in a range
// failure's "fields" map iteration -- deterministic output.
type retentionField struct {
	name     string
	min, max int
}

var retentionFields = []retentionField{
	{"r1_hours", 1, 168},
	{"r2_days", 1, 90},
	{"r3_days", 30, 1095},
	{"size_cap_mb", 64, 4096},
}

// retentionValues extracts a RetentionSettings' four fields into a
// wire-name-keyed map, the shape both validation and Set iterate over.
func retentionValues(r RetentionSettings) map[string]int {
	return map[string]int{
		"r1_hours":    r.R1Hours,
		"r2_days":     r.R2Days,
		"r3_days":     r.R3Days,
		"size_cap_mb": r.SizeCapMB,
	}
}

// overriddenNames returns the subset of retentionFields' names that are
// true in overridden, in retentionFields' fixed order -- deterministic,
// unlike a map iteration.
func overriddenNames(overridden map[string]bool) []string {
	out := []string{}
	for _, f := range retentionFields {
		if overridden[f.name] {
			out = append(out, f.name)
		}
	}
	return out
}

type settingsGetResponse struct {
	Retention     RetentionSettings `json:"retention"`
	EnvOverridden []string          `json:"env_overridden"`
}

// handleSettingsGet serves GET /api/settings. Options.Settings is nil in
// tests that don't wire one -- unlike Logs, a zero-valued, unlocked
// retention object is a meaningful (if uninteresting) "empty" response
// here, so this degrades to that rather than 404.
func (s *Server) handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Settings == nil {
		writeJSON(w, settingsGetResponse{EnvOverridden: []string{}})
		return
	}
	ret, overridden := s.opts.Settings.Get()
	writeJSON(w, settingsGetResponse{Retention: ret, EnvOverridden: overriddenNames(overridden)})
}

// settingsPutRequest is decoded with DisallowUnknownFields, so any key
// besides "retention" at the top level, or besides the four
// RetentionSettings fields inside it, fails the whole decode -- the
// "whitelist EXACTLY those four keys" contract, enforced by the decoder
// itself rather than hand-rolled field-by-field checking.
type settingsPutRequest struct {
	Retention RetentionSettings `json:"retention"`
}

// handleSettingsPut serves PUT /api/settings. Options.Settings is nil in
// tests that don't wire one; unlike GET, there's no meaningful no-op
// success for a write with nowhere to write to, so this answers 404 the
// same way Logs does for the same reason.
//
// Validation order: decode/whitelist (400) -> env-lock (409) -> range
// (400) -> persist. Env-lock is checked before range so a field that's
// both out-of-range AND env-overridden reports the more actionable
// "you can't change this at all" rather than a range error the caller
// might otherwise "fix" and resubmit, still fruitlessly.
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	if s.opts.Settings == nil {
		writeError(w, http.StatusNotFound, "settings unavailable")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body settingsPutRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	_, overridden := s.opts.Settings.Get()
	if locked := overriddenNames(overridden); len(locked) > 0 {
		writeJSONStatus(w, http.StatusConflict, struct {
			Error         string   `json:"error"`
			EnvOverridden []string `json:"env_overridden"`
		}{"env-overridden fields cannot be changed here", locked})
		return
	}

	values := retentionValues(body.Retention)
	fieldErrs := map[string]string{}
	for _, f := range retentionFields {
		if v := values[f.name]; v < f.min || v > f.max {
			fieldErrs[f.name] = fmt.Sprintf("must be between %d and %d", f.min, f.max)
		}
	}
	if len(fieldErrs) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, struct {
			Error  string            `json:"error"`
			Fields map[string]string `json:"fields"`
		}{"invalid retention settings", fieldErrs})
		return
	}

	for _, f := range retentionFields {
		if err := s.opts.Settings.Set(f.name, values[f.name]); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	ret, overridden := s.opts.Settings.Get()
	writeJSON(w, settingsGetResponse{Retention: ret, EnvOverridden: overriddenNames(overridden)})
}
