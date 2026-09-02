package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Overview layout: which of the Overview's rearrangeable modules sit in
// which of its two lanes, in what order, which the owner has hidden, how
// those two lanes split the band's width, and how tall each elastic
// module is allowed to be -- the persisted form of that page's own
// "Customize" edit mode.
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
// (see its own doc), which is why it stayed at 1 through a module-set
// change.
//
// 1 -> 2 IS a real shape change: the constrained-resize pass added Ratio
// and Sizes. Both are additive and both have a defined "absent" value
// (see clampOverviewRatio and mergeOverviewLayout), so the migration is
// a fill-in-the-defaults, not a rewrite -- a stored v1 document, or a PUT
// from a browser still holding a cached v1 bundle, is accepted and comes
// back as a v2 document at the default split with every module at
// "normal". Anything NEWER than this constant is refused outright (see
// validateOverviewLayout): re-encoding a document whose shape we cannot
// read would destroy it.
const overviewLayoutVersion = 2

// The wide lane's share of the modules band, as a fraction of the two
// lanes' combined flex space -- the one number the Customize divider
// persists.
//
// The bounds are the range over which BOTH lanes still read as
// themselves against real content. Below 0.60 the wide lane's two
// stacked modules (Top Consumers' name/bar/value grid, the events feed's
// own three-column rows) start wrapping while the rail -- four bare
// label+sparkline rows, the one module here that is genuinely narrow by
// nature -- sits in space it has no use for. Above 0.75 the rail's own
// tiles fall under ~270px at the app's usual desktop width (a 1440px
// viewport less the ~15rem sidebar and the page gutters), which is where
// its paired value+sparkline rows begin to crowd.
//
// The default is today's split exactly: the band shipped as
// `flex: 1.6 1 0` against `flex: 1 1 0`, i.e. 1.6/2.6 = 0.6154, rounded
// to three places for a wire value that reads as a number rather than a
// repeating fraction. The 0.4px that rounding costs at 1150px of band is
// deliberately beneath notice -- what matters is that a never-customized
// install renders the band it always did.
const (
	overviewRatioMin     = 0.60
	overviewRatioMax     = 0.75
	overviewRatioDefault = 0.615
)

// The three height steps a resizable module can be set to. Wire values,
// like the lane names above. "normal" is the default and is stored by
// ABSENCE from the Sizes map (see mergeOverviewLayout), so a document
// that customizes nothing carries no sizes at all.
const (
	overviewSizeCompact = "compact"
	overviewSizeNormal  = "normal"
	overviewSizeTall    = "tall"
)

func knownOverviewSize(size string) bool {
	switch size {
	case overviewSizeCompact, overviewSizeNormal, overviewSizeTall:
		return true
	}
	return false
}

// The modules band's two lanes. These are wire values (they appear
// inside saved documents), not just internal labels, so they are fixed
// strings rather than an iota enum.
const (
	overviewColumnWide   = "wide"
	overviewColumnNarrow = "narrow"
)

// overviewModule is one rearrangeable module's identity: the stable
// string id every saved document refers to it by, the lane it belongs to
// when nothing has been saved yet, and whether it has an elastic body a
// height step can act on.
//
// The id is deliberately a hand-picked slug rather than anything derived
// from a CSS class or a component name -- those are free to be renamed
// or restyled, and a saved layout must survive both.
type overviewModule struct {
	ID     string
	Column string
	// Resizable: this module's body is a list whose length is the whole
	// point of it (a leaderboard, a feed), so "compact/normal/tall" buys
	// real content rather than padding. A module without one carries no
	// size, and a size stored against it is dropped by the merge the same
	// way an unknown id is -- the client half (web/src/lib/overviewLayout.ts)
	// holds the matching flag and the actual row counts, since how many
	// rows a step buys is a rendering decision, not a storage one.
	Resizable bool
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
//
// metrics-rail is deliberately NOT resizable: its four tiles are a fixed
// label + value + 28px sparkline each, with nothing that reads better at
// another height -- a taller rail is the same four tiles with more air
// between them, which is the dead space this page's own layout passes
// have spent three rounds deleting.
var overviewModules = []overviewModule{
	{ID: "top-consumers", Column: overviewColumnWide, Resizable: true},
	{ID: "events", Column: overviewColumnWide, Resizable: true},
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
//
// Ratio is ONE number for the whole band, not a width per lane: the two
// lanes divide one row, so a second number could only ever disagree with
// the first. 0 means "not specified" -- what a v1 document and a curl
// that omits the field both look like -- and resolves to
// overviewRatioDefault.
//
// Sizes maps a module id to its height step, and only ever carries the
// NON-default ones: "normal" is absence, so a document nobody has
// resized carries an empty map. That keeps the stored blob honest about
// what was actually customized, and makes "is this the default layout?"
// (the Reset control's own disabled state) a plain emptiness check on
// the client.
type OverviewLayout struct {
	Version int               `json:"version"`
	Wide    []string          `json:"wide"`
	Narrow  []string          `json:"narrow"`
	Hidden  []string          `json:"hidden"`
	Ratio   float64           `json:"ratio"`
	Sizes   map[string]string `json:"sizes"`
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

func resizableOverviewModule(id string) bool {
	for _, m := range overviewModules {
		if m.ID == id {
			return m.Resizable
		}
	}
	return false
}

// clampOverviewRatio is the storage-side repair half of the ratio rule --
// the counterpart to validateOverviewLayout's outright refusal, and the
// same split mergeOverviewLayout already draws between a blob that
// arrives from STORAGE (repair it; there is no caller left to tell) and
// one that arrives from a PUT (400 it).
//
// 0 is the one value that is not out of range but absent: a v1 document
// has no ratio at all, and neither does a hand-rolled curl that only
// wanted to reorder something. Both mean "whatever the default is".
func clampOverviewRatio(ratio float64) float64 {
	switch {
	case ratio == 0:
		return overviewRatioDefault
	case ratio < overviewRatioMin:
		return overviewRatioMin
	case ratio > overviewRatioMax:
		return overviewRatioMax
	default:
		return ratio
	}
}

// defaultOverviewLayout is the arrangement a fresh install renders and
// the one "Reset layout" restores: every known module in its own default
// column, in table order, nothing hidden, the band at its default split,
// every module at "normal".
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
// It is also the v1 -> v2 MIGRATION, and it is a migration precisely
// because both new fields have a defined "absent":
//
//   - Ratio 0 (a v1 document, or an omitted field) becomes the default
//     split; anything out of range is clamped rather than refused, since
//     storage has no caller to 400 (clampOverviewRatio's own doc).
//   - Sizes is normalized to its canonical form: an unknown module id is
//     dropped exactly the way an unknown id in a lane is, a module with
//     no elastic body (metrics-rail) is dropped because a size means
//     nothing there, an unrecognized step normalizes to "normal", and
//     "normal" itself is dropped because absence IS normal. A v1
//     document simply has none of these and comes out with an empty map.
//
// A PUT from an older SPA therefore resets Ratio and Sizes to their
// defaults rather than preserving what is stored. That is the
// whole-document-replace contract working as designed, not a gap: this
// function is pure and has only the submitted document to work from, and
// the alternative -- silently merging a stale client's document with
// fields it doesn't know exist -- would make what got saved depend on
// which bundle the browser happened to be holding. (The lane lists get
// their append-the-missing rule instead because a module can be ADDED by
// a release, which is a different problem: there, the client's omission
// means "I have never heard of this", not "I do not want it".)
//
// The result always carries THIS build's version, three real (never nil)
// slices and a real (never nil) map, so the JSON never contains `null`.
func mergeOverviewLayout(stored OverviewLayout) OverviewLayout {
	merged := OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{},
		Narrow:  []string{},
		Hidden:  []string{},
		Ratio:   clampOverviewRatio(stored.Ratio),
		Sizes:   map[string]string{},
	}
	for id, size := range stored.Sizes {
		if !resizableOverviewModule(id) || !knownOverviewSize(size) || size == overviewSizeNormal {
			continue
		}
		merged.Sizes[id] = size
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
// An omitted version (0) is accepted and stamped by the merge, and so is
// any OLDER version this build still knows how to migrate -- v1, today,
// which mergeOverviewLayout fills out with the new fields' defaults. Only
// a version NEWER than this build is refused, because re-encoding a
// document whose shape we cannot read would destroy it.
//
// Ratio and Sizes take the same reject-don't-repair posture the ids do,
// with one deliberate exception each. Ratio: 0 means absent (a v1
// document, or a curl that only wanted to reorder), so only a NON-zero
// out-of-range number is a caller bug. Sizes: an unknown module id and an
// unrecognized step are both refused by name, but a size against a KNOWN
// module that simply has no elastic body (metrics-rail) is not -- a
// client sending one size per module is being reasonable, and the merge
// drops it, which the PUT's own response then shows.
func validateOverviewLayout(l OverviewLayout) error {
	if l.Version < 0 || l.Version > overviewLayoutVersion {
		return fmt.Errorf("unsupported layout version %d: this build understands up to version %d", l.Version, overviewLayoutVersion)
	}
	if l.Ratio != 0 && (l.Ratio < overviewRatioMin || l.Ratio > overviewRatioMax) {
		return fmt.Errorf("overview column ratio %g out of range: must be between %g and %g", l.Ratio, overviewRatioMin, overviewRatioMax)
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
	for id, size := range l.Sizes {
		if !knownOverviewModule(id) {
			return fmt.Errorf("unknown overview module %q", id)
		}
		if !knownOverviewSize(size) {
			return fmt.Errorf("unknown overview module size %q for module %q", size, id)
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
