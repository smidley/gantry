package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Overview layout: which of the Overview's rearrangeable modules sit in
// which of its two lanes, in what order, and which the owner has hidden
// -- the persisted form of that page's own "Customize" edit mode.
//
// Modeled directly on /api/groups (api_groups.go): ONE whole-document
// envelope shared by GET's response, PUT's request, and PUT's own
// response, with no per-module create/move/hide route. The UI always
// submits its already-edited full document, and the server answers with
// what it actually stored -- so a client never has to reconstruct the
// saved state from a diff it sent.
//
// Why server-side at all, when the Overview's other preferences
// (topResource, theme, motion) live in localStorage: those are
// per-BROWSER view state, and being wrong on a second browser costs
// nothing. A layout is an arrangement the owner deliberately built; it
// has to follow them from the desktop they built it on to the phone
// they check the box from, which only the config DB can do.

// overviewLayoutVersion is the document's own schema version, carried on
// the wire so a later shape change has something to migrate FROM rather
// than having to guess at an untagged blob. Deliberately NOT bumped when
// a module is added to or removed from overviewModules below --
// mergeOverviewLayout already absorbs both of those without a migration
// (see its own doc), which is the entire reason this number has stayed
// at 1 through a module-set change.
const overviewLayoutVersion = 1

// The modules band's two lanes. These are wire values (they appear
// inside saved documents), not just internal labels, so they are fixed
// strings rather than an iota enum.
const (
	overviewColumnWide   = "wide"
	overviewColumnNarrow = "narrow"
)

// overviewModule is one rearrangeable module's identity: the stable
// string id every saved document refers to it by, plus the lane it
// belongs to when nothing has been saved yet.
//
// The id is deliberately a hand-picked slug rather than anything derived
// from a CSS class or a component name -- those are free to be renamed
// or restyled, and a saved layout must survive both.
type overviewModule struct {
	ID     string
	Column string
}

// overviewModules is the CLOSED known set: an id outside this table is
// rejected on the way in (validateOverviewLayout) and dropped on the way
// out (mergeOverviewLayout). Slice order IS the default order within
// each module's own column, so the table reads top-to-bottom exactly as
// the default page does: Top Consumers over Recent events in the wide
// lane, the stat-tile rail alone in the narrow one.
//
// Granularity is one entry per top-level module -- the rail is a single
// module, not its four tiles. Anything OUTSIDE the modules band (the
// status headline, the attention callouts, the fleet strip, the bay
// schematic, the all-clear band, the GPU strip) is deliberately absent:
// those are pinned, because the "needs you" surface must not be
// buryable and the GPU strip is its own full-width row below the band.
var overviewModules = []overviewModule{
	{ID: "top-consumers", Column: overviewColumnWide},
	{ID: "events", Column: overviewColumnWide},
	{ID: "metrics-rail", Column: overviewColumnNarrow},
}

// OverviewLayout is the one wire shape for GET/PUT /api/layout/overview
// -- request and response alike, the groupsResponse convention.
//
// Wide/Narrow are the two lanes' ordered id lists; Hidden holds the ids
// the owner has switched off. Every id appears at most ONCE across all
// three lists: a hidden module has no position, which is why bringing
// one back places it at its default (the same rule a brand-new module
// gets -- see mergeOverviewLayout).
type OverviewLayout struct {
	Version int      `json:"version"`
	Wide    []string `json:"wide"`
	Narrow  []string `json:"narrow"`
	Hidden  []string `json:"hidden"`
}

// LayoutIface is the minimal store surface /api/layout/overview needs
// (main wires a small adapter over *store.Store, JSON-blob-encoded into
// the same generic settings table Settings and Groups already share),
// kept this narrow for the same reason GroupsIface is.
type LayoutIface interface {
	// Get returns the saved document exactly as it was last persisted.
	// A never-saved install returns the ZERO value, not an error --
	// mergeOverviewLayout turns that into the default layout, so
	// "nothing saved yet" needs no separate signal.
	Get() (OverviewLayout, error)
	// Set replaces the whole document in one write. There is no
	// per-module endpoint; the UI always PUTs its own already-edited
	// full document, the same whole-document-replace contract
	// GroupsIface.Set describes. Called only with an
	// already-validated, already-merged document.
	Set(OverviewLayout) error
}

func knownOverviewModule(id string) bool {
	for _, m := range overviewModules {
		if m.ID == id {
			return true
		}
	}
	return false
}

// defaultOverviewLayout is the arrangement a fresh install renders and
// the one "Reset layout" restores: every known module in its own default
// column, in table order, nothing hidden.
func defaultOverviewLayout() OverviewLayout {
	return mergeOverviewLayout(OverviewLayout{})
}

// mergeOverviewLayout reconciles a STORED document against the module
// set THIS build knows about. It is the whole forward/backward-
// compatibility story, and it is pure so it can be reasoned about (and
// tested) without any HTTP or store plumbing:
//
//   - An id this build doesn't know is dropped, silently. That covers
//     both a document written by a NEWER build (a module that doesn't
//     exist here yet) and one written before a module was retired.
//   - An id this build knows that the document never mentions is
//     placed, so a release that adds a module still shows it by default
//     to everyone who already saved a layout. It lands at the END of its
//     own default column rather than at its default index: the existing
//     order is the owner's deliberate arrangement, and a module they
//     have never seen has not earned the right to shove itself above it.
//     A module already listed as Hidden counts as placed -- it stays
//     hidden.
//   - A duplicate id keeps its first occurrence and drops the rest.
//     validateOverviewLayout already rejects duplicates on the way in,
//     so this only ever fires for a blob edited outside the app; the
//     alternative (trusting it) would render one module twice and
//     violate the keyed-each contract the SPA renders with.
//
// The result always carries THIS build's version and three real (never
// nil) slices, so the JSON never contains `null` for a list.
func mergeOverviewLayout(stored OverviewLayout) OverviewLayout {
	merged := OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{},
		Narrow:  []string{},
		Hidden:  []string{},
	}

	placed := make(map[string]bool, len(overviewModules))
	keep := func(dst *[]string, src []string) {
		for _, id := range src {
			if !knownOverviewModule(id) || placed[id] {
				continue
			}
			placed[id] = true
			*dst = append(*dst, id)
		}
	}
	keep(&merged.Wide, stored.Wide)
	keep(&merged.Narrow, stored.Narrow)
	keep(&merged.Hidden, stored.Hidden)

	for _, m := range overviewModules {
		if placed[m.ID] {
			continue
		}
		placed[m.ID] = true
		if m.Column == overviewColumnNarrow {
			merged.Narrow = append(merged.Narrow, m.ID)
			continue
		}
		merged.Wide = append(merged.Wide, m.ID)
	}
	return merged
}

// validateOverviewLayout enforces PUT's whitelist, the same posture
// handleAlertsRulesPut takes with rule ids: every id must come from the
// closed known set, and no id may appear twice across the three lists.
// Both failures are the caller's bug, not a recoverable state, so they
// 400 with a message naming the offending id rather than being quietly
// repaired -- mergeOverviewLayout's own repair exists for blobs that
// arrive from STORAGE, where there is no caller left to tell.
//
// An omitted version (0) is accepted and stamped by the merge; any other
// version this build doesn't implement is refused, because re-encoding a
// document whose shape we cannot read would destroy it.
func validateOverviewLayout(l OverviewLayout) error {
	if l.Version != 0 && l.Version != overviewLayoutVersion {
		return fmt.Errorf("unsupported layout version %d: this build understands version %d", l.Version, overviewLayoutVersion)
	}
	seen := map[string]bool{}
	for _, list := range [][]string{l.Wide, l.Narrow, l.Hidden} {
		for _, id := range list {
			if !knownOverviewModule(id) {
				return fmt.Errorf("unknown overview module %q", id)
			}
			if seen[id] {
				return fmt.Errorf("overview module %q listed more than once", id)
			}
			seen[id] = true
		}
	}
	return nil
}

// handleOverviewLayoutGet serves GET /api/layout/overview. Options.Layout
// is nil in tests that don't wire one -- like Groups (and unlike Logs),
// there is a meaningful answer for "no store": the default layout, which
// is also exactly what a wired-but-never-written store returns.
//
// The response is always the MERGED document, never the raw stored one:
// the SPA renders straight off this, so the drop-unknown/append-missing
// rule has to have already run by the time it arrives. The merge result
// is deliberately not written back -- a read stays a read, and the next
// real PUT persists it anyway.
func (s *Server) handleOverviewLayoutGet(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Layout == nil {
		writeJSON(w, defaultOverviewLayout())
		return
	}
	stored, err := s.opts.Layout.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, mergeOverviewLayout(stored))
}

// handleOverviewLayoutPut serves PUT /api/layout/overview. Options.Layout
// is nil in tests that don't wire one; unlike GET there is no meaningful
// no-op success for a write with nowhere to write to, so this 404s the
// way Settings' and Groups' own PUTs do.
//
// No confirm header, and not gated by GANTRY_READ_ONLY -- matching
// Groups (and through it Settings) exactly: this is config-shaped
// preference data, not a destructive mutation of real containers or
// images the way Images/ContainersMaintenance's remove/prune routes are.
// The mux-wide cross-site header check in gate.go still applies, as it
// does to every mutating route.
//
// The submitted document is merged before it is stored, not just before
// it is returned: a client that omits a module (an older SPA against a
// newer binary) must not silently delete it from the saved document.
func (s *Server) handleOverviewLayoutPut(w http.ResponseWriter, r *http.Request) {
	if s.opts.Layout == nil {
		writeError(w, http.StatusNotFound, "layout unavailable")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body OverviewLayout
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	if err := validateOverviewLayout(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	merged := mergeOverviewLayout(body)
	if err := s.opts.Layout.Set(merged); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stored, err := s.opts.Layout.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, mergeOverviewLayout(stored))
}
