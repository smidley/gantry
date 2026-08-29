package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Group is one user-defined container group: a named set of container
// names the Compare view can be pre-filled with in one click -- the
// custom, user-named analogue of composeGroups.ts's own docker-compose-
// derived groups (web/src/lib/composeGroups.ts), persisted server-side
// instead of derived from live container metadata.
type Group struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// GroupsIface is the minimal store surface /api/groups needs (main
// wires a small adapter over *store.Store, JSON-blob-encoded into the
// same generic settings table Settings itself uses -- see
// groupsAdapter's own doc), kept this narrow for the same reason
// SettingsIface is.
type GroupsIface interface {
	// Get returns every saved group, in the order they were last
	// persisted -- empty, non-nil, when nothing's been saved yet.
	Get() ([]Group, error)
	// Set replaces the entire saved list in one write. There's no
	// per-group create/rename/delete endpoint; the UI always PUTs its
	// own already-edited full list, the same whole-document-replace
	// contract every other settings-shaped resource in this app uses.
	// Called only after validateGroups has already approved the list.
	Set(groups []Group) error
}

// maxGroups/maxGroupMembers are generous caps meant to catch a
// malformed client, not to constrain a real user's actual usage.
const (
	maxGroups       = 50
	maxGroupMembers = 50
)

// validateGroups enforces /api/groups' own PUT whitelist: every group
// name must be non-empty and unique (case-sensitive, same as every
// other name comparison in this app), and both the group count and
// each group's own member count are capped.
func validateGroups(groups []Group) error {
	if len(groups) > maxGroups {
		return fmt.Errorf("too many groups: max %d", maxGroups)
	}
	seen := make(map[string]bool, len(groups))
	for _, g := range groups {
		if g.Name == "" {
			return fmt.Errorf("group name must not be empty")
		}
		if seen[g.Name] {
			return fmt.Errorf("duplicate group name %q", g.Name)
		}
		seen[g.Name] = true
		if len(g.Members) > maxGroupMembers {
			return fmt.Errorf("group %q: too many members: max %d", g.Name, maxGroupMembers)
		}
	}
	return nil
}

// groupsResponse is /api/groups' one wire shape: GET's response, PUT's
// request, and PUT's own response all share this exact
// {"groups":[...]} envelope -- unlike /api/settings, there's no second
// field (like env_overridden) that would make GET and PUT diverge, so
// one struct serves all three, decoded with DisallowUnknownFields on
// the way in the same as settingsPutRequest.
type groupsResponse struct {
	Groups []Group `json:"groups"`
}

// handleGroupsGet serves GET /api/groups. Options.Groups is nil in
// tests that don't wire one -- same as Settings, an empty groups list
// is a meaningful "empty" response here, not a 404.
func (s *Server) handleGroupsGet(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Groups == nil {
		writeJSON(w, groupsResponse{Groups: []Group{}})
		return
	}
	groups, err := s.opts.Groups.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, groupsResponse{Groups: groups})
}

// handleGroupsPut serves PUT /api/groups. Options.Groups is nil in
// tests that don't wire one; unlike GET, there's no meaningful no-op
// success for a write with nowhere to write to, so this 404s the same
// way Settings' own PUT does.
//
// No confirm header, and not gated by GANTRY_READ_ONLY -- matching
// Settings' own PUT (config-shape data, not a destructive mutation of
// real containers/images), not Images/ContainersMaintenance's own
// remove/prune routes, which check both. A future read-only mode that
// also wants to lock this down would need to opt it in explicitly;
// today it mirrors Settings exactly.
func (s *Server) handleGroupsPut(w http.ResponseWriter, r *http.Request) {
	if s.opts.Groups == nil {
		writeError(w, http.StatusNotFound, "groups unavailable")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body groupsResponse
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	if err := validateGroups(body.Groups); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.opts.Groups.Set(body.Groups); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	groups, err := s.opts.Groups.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, groupsResponse{Groups: groups})
}
