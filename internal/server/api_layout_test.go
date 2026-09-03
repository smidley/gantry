package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeLayout is a minimal in-memory LayoutIface -- the test double every
// /api/layout/overview test in this file wires in place of main's real
// store-backed adapter, mirroring api_groups_test.go's own fakeGroups.
type fakeLayout struct {
	layout   OverviewLayout
	getErr   error
	setErr   error
	setCalls []OverviewLayout
}

func (f *fakeLayout) Get() (OverviewLayout, error) {
	if f.getErr != nil {
		return OverviewLayout{}, f.getErr
	}
	return f.layout, nil
}

func (f *fakeLayout) Set(l OverviewLayout) error {
	f.setCalls = append(f.setCalls, l)
	if f.setErr != nil {
		return f.setErr
	}
	f.layout = l
	return nil
}

// --- mergeOverviewLayout (pure) ------------------------------------------
//
// The forward/backward-compatibility rule lives entirely in this one
// function, so it gets its own table of cases independent of any HTTP
// plumbing -- an unknown id must vanish, a known id the stored document
// never mentions must still be placed, and neither may ever produce a
// duplicate.

func TestMergeOverviewLayoutEmptyDocumentIsTheDefaultLayout(t *testing.T) {
	require.Equal(t, defaultOverviewLayout(), mergeOverviewLayout(OverviewLayout{}))
}

func TestDefaultOverviewLayoutPlacesEveryKnownModuleExactlyOnce(t *testing.T) {
	def := defaultOverviewLayout()
	require.Equal(t, overviewLayoutVersion, def.Version)
	require.Empty(t, def.Hidden, "nothing is hidden by default")

	seen := map[string]int{}
	for _, id := range append(append([]string{}, def.Wide...), def.Narrow...) {
		seen[id]++
	}
	require.Len(t, seen, len(overviewModules))
	for _, m := range overviewModules {
		require.Equal(t, 1, seen[m.ID], "module %q must appear exactly once in the default layout", m.ID)
	}
}

func TestMergeOverviewLayoutDropsUnknownIDsSilently(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events", "a-module-from-the-future", "top-consumers"},
		Narrow:  []string{"storage"},
		Hidden:  []string{"another-ghost"},
	})
	require.Equal(t, []string{"events", "top-consumers"}, merged.Wide)
	require.Equal(t, []string{"storage"}, merged.Narrow)
	require.Empty(t, merged.Hidden)
}

// TestMergeOverviewLayoutAppendsMissingKnownIDs is the release-adds-a-
// module case: a document saved by an older build knows nothing about
// whatever ships next, and that module must still show up -- in its own
// default column, appended after whatever the user already arranged
// there (see mergeOverviewLayout's own doc for why appended rather than
// inserted at its default index).
func TestMergeOverviewLayoutAppendsMissingKnownIDsAtTheirDefaultColumn(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{Version: overviewLayoutVersion, Wide: []string{"events"}})
	require.Equal(t, []string{"events", "top-consumers"}, merged.Wide, "the missing wide module lands at the end of its own column")
	require.Equal(t, []string{"storage"}, merged.Narrow, "the missing narrow module lands in the narrow column, not the wide one")
	require.Empty(t, merged.Hidden)
}

// TestMergeOverviewLayoutKeepsAHiddenModuleHidden pins that a module the
// user hid is NOT re-placed into a column by the missing-id rule: it is
// already accounted for, just in the hidden list.
func TestMergeOverviewLayoutKeepsAHiddenModuleHidden(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"top-consumers"},
		Narrow:  []string{"storage"},
		Hidden:  []string{"events"},
	})
	require.Equal(t, []string{"top-consumers"}, merged.Wide)
	require.Equal(t, []string{"storage"}, merged.Narrow)
	require.Equal(t, []string{"events"}, merged.Hidden)
}

func TestMergeOverviewLayoutDeduplicatesFirstOccurrenceWins(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events", "events", "storage"},
		Narrow:  []string{"storage", "top-consumers"},
		Hidden:  []string{"events"},
	})
	require.Equal(t, []string{"events", "storage"}, merged.Wide)
	require.Equal(t, []string{"top-consumers"}, merged.Narrow)
	require.Empty(t, merged.Hidden)
}

// TestMergeOverviewLayoutPreservesUserOrder is the whole point of saving
// a layout at all -- a fully-populated document comes back byte-for-byte
// as it went in, columns swapped and all.
func TestMergeOverviewLayoutPreservesUserOrder(t *testing.T) {
	stored := OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"storage", "events"},
		Narrow:  []string{"top-consumers"},
		Hidden:  []string{},
		Ratio:   0.7,
		Sizes:   map[string]string{"events": overviewSizeTall},
	}
	require.Equal(t, stored, mergeOverviewLayout(stored))
}

// TestMergeOverviewLayoutNeverReturnsNilSlices keeps the JSON wire shape
// honest: a nil []string marshals to `null`, which the SPA would have to
// guard on every read. Every list is always a real (if empty) array, and
// Sizes a real (if empty) object.
func TestMergeOverviewLayoutNeverReturnsNilSlices(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{})
	require.NotNil(t, merged.Wide)
	require.NotNil(t, merged.Narrow)
	require.NotNil(t, merged.Hidden)
	require.NotNil(t, merged.Sizes)
}

// --- v1 -> v2 migration (pure) --------------------------------------------
//
// The constrained-resize pass added Ratio and Sizes and bumped the
// document to 2. Both are additive with a defined "absent", so the
// migration is a fill-in-the-defaults -- these pin that a document
// written by the shipped v1 build (or by a browser still holding a
// cached v1 bundle) survives it untouched apart from those defaults.

func TestMergeOverviewLayoutMigratesAV1Document(t *testing.T) {
	// Exactly what api_layout.go@v1 stored: three lists, no ratio, no
	// sizes, and the owner's own arrangement inside them.
	v1 := OverviewLayout{
		Version: 1,
		Wide:    []string{"events", "top-consumers"},
		Narrow:  []string{"storage"},
		Hidden:  []string{},
	}
	require.Equal(t, OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events", "top-consumers"},
		Narrow:  []string{"storage"},
		Hidden:  []string{},
		Ratio:   overviewRatioDefault,
		Sizes:   map[string]string{},
	}, mergeOverviewLayout(v1), "the arrangement survives; only the new fields are filled")
}

func TestMergeOverviewLayoutRatio(t *testing.T) {
	for name, tc := range map[string]struct {
		stored float64
		want   float64
	}{
		"absent (a v1 document, or an omitted field) takes the default": {stored: 0, want: overviewRatioDefault},
		"an in-range ratio is kept exactly":                             {stored: 0.7, want: 0.7},
		"the lower bound is in range":                                   {stored: overviewRatioMin, want: overviewRatioMin},
		"the upper bound is in range":                                   {stored: overviewRatioMax, want: overviewRatioMax},
		"too narrow a wide lane clamps up":                              {stored: 0.2, want: overviewRatioMin},
		"too wide a wide lane clamps down":                              {stored: 0.98, want: overviewRatioMax},
		"a nonsense negative clamps up rather than throwing":            {stored: -3, want: overviewRatioMin},
	} {
		t.Run(name, func(t *testing.T) {
			require.InDelta(t, tc.want, mergeOverviewLayout(OverviewLayout{Ratio: tc.stored}).Ratio, 1e-9)
		})
	}
}

func TestMergeOverviewLayoutSizes(t *testing.T) {
	for name, tc := range map[string]struct {
		stored map[string]string
		want   map[string]string
	}{
		"absent (a v1 document) is every module at normal": {
			stored: nil,
			want:   map[string]string{},
		},
		"a real step against a resizable module is kept": {
			stored: map[string]string{"events": overviewSizeTall, "top-consumers": overviewSizeCompact},
			want:   map[string]string{"events": overviewSizeTall, "top-consumers": overviewSizeCompact},
		},
		"an explicit normal is dropped -- absence IS normal": {
			stored: map[string]string{"events": overviewSizeNormal},
			want:   map[string]string{},
		},
		"an unrecognized step normalizes to normal, i.e. is dropped": {
			stored: map[string]string{"events": "enormous"},
			want:   map[string]string{},
		},
		"an unknown module id is dropped, the same as in a lane": {
			stored: map[string]string{"a-module-from-the-future": overviewSizeTall},
			want:   map[string]string{},
		},
		"a size against a module with no elastic body is dropped": {
			stored: map[string]string{"storage": overviewSizeTall},
			want:   map[string]string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, mergeOverviewLayout(OverviewLayout{Sizes: tc.stored}).Sizes)
		})
	}
}

// TestDefaultOverviewLayoutIsUnresized pins the other half of "everything
// at normal is exactly today's page": the default document carries the
// band's shipped split and no sizes at all.
func TestDefaultOverviewLayoutIsUnresized(t *testing.T) {
	def := defaultOverviewLayout()
	require.InDelta(t, overviewRatioDefault, def.Ratio, 1e-9)
	require.Empty(t, def.Sizes)
}

// --- module-set migration (pure) ------------------------------------------
//
// overviewModules changed once more since the section above was written:
// the metrics rail retired (pinned at the top of the page now, no longer
// arrangeable) and storage took its place in the narrow lane (see
// overviewModules' own doc for the why). The document version does not
// move for this, for the same reason it never has for a module-set
// change (see overviewLayoutVersion's own doc): the drop-unknown/append-
// missing rule above was already the whole migration story before today,
// because a retired id is indistinguishable from one a future build
// invented -- this build doesn't know either, and treats both the same.
// What is worth its own test is not a new mechanism, only this specific
// pair of ids going through the old one: a real owner's stored v2
// document -- Ratio and Sizes already filled in, nothing left to migrate
// on that front -- that still names the rail.

// TestMergeOverviewLayoutMigratesTheRetiredMetricsRailModule pins the
// arranged case: the rail is gone from wherever it was named, storage
// takes its default place in the narrow lane, and nothing else about the
// document -- the wide lane's own order, the owner's ratio, the owner's
// sizes, the version -- moves at all.
func TestMergeOverviewLayoutMigratesTheRetiredMetricsRailModule(t *testing.T) {
	stored := OverviewLayout{
		Version: overviewLayoutVersion,               // already v2 before today -- not a field migration
		Wide:    []string{"events", "top-consumers"}, // the owner's own order, not table order
		Narrow:  []string{"metrics-rail"},
		Hidden:  []string{},
		Ratio:   0.7,
		Sizes:   map[string]string{"events": overviewSizeTall},
	}
	merged := mergeOverviewLayout(stored)

	require.Equal(t, overviewLayoutVersion, merged.Version, "a module-set change was never a version bump, before today or now")
	require.Equal(t, []string{"events", "top-consumers"}, merged.Wide, "the wide lane is untouched by a narrow-lane retirement")
	require.Equal(t, []string{"storage"}, merged.Narrow, "the retired id is dropped and storage is appended at its default")
	require.Empty(t, merged.Hidden)
	require.InDelta(t, 0.7, merged.Ratio, 1e-9, "the owner's ratio is untouched by a module-set change")
	require.Equal(t, map[string]string{"events": overviewSizeTall}, merged.Sizes, "the owner's sizes are untouched by a module-set change")

	for _, list := range [][]string{merged.Wide, merged.Narrow, merged.Hidden} {
		require.NotContains(t, list, "metrics-rail", "the retired id must survive in no list at all")
	}
}

// TestMergeOverviewLayoutMigratesARetiredHiddenModule is the same
// migration from the other starting point: an owner who had switched the
// rail off, rather than arranged it. It must still lose that entry --
// Hidden is not a place a retired id gets to wait out its own removal --
// and storage must still arrive, visible, at its default, exactly as it
// would for an owner who had never touched the rail at all.
func TestMergeOverviewLayoutMigratesARetiredHiddenModule(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"top-consumers", "events"},
		Narrow:  []string{},
		Hidden:  []string{"metrics-rail"},
	})
	require.Equal(t, overviewLayoutVersion, merged.Version, "a module-set change was never a version bump, before today or now")
	require.Equal(t, []string{"storage"}, merged.Narrow, "storage is placed at its default even though the retired id was hidden, not arranged")
	require.Empty(t, merged.Hidden, "the retired id does not linger in Hidden")
}

// --- GET ------------------------------------------------------------------

func TestOverviewLayoutGetNilOptionReturnsDefaultsNotPanic(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Layout left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, defaultOverviewLayout(), body)
}

func TestOverviewLayoutGetNeverSavedReturnsDefaults(t *testing.T) {
	fl := &fakeLayout{} // zero value: nothing ever persisted
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, defaultOverviewLayout(), body)
}

// TestOverviewLayoutGetDefaultWireDocumentIsUnchanged pins the default
// document as a literal, on the wire, rather than against
// defaultOverviewLayout() -- which the test above already does and which
// would happily agree with itself through any module change.
//
// It exists because the Overview's layout keeps moving while this
// document is supposed to stand still. The metrics rail sits in the
// status band's right column now rather than in a full-width row above
// the headline (Scott: "CPU/mem/net/io should be pinned at the top
// right"), and the fleet strip went back to its fixed-pitch pills --
// both PINNED regions, neither a module. So the inventory is still the
// same three modules in the same two lanes at v2, and a saved layout
// needs no migration for either change. If this test has to be edited,
// something that was meant to be pinned became rearrangeable (or the
// reverse), and that is a decision with a stored document behind it.
func TestOverviewLayoutGetDefaultWireDocumentIsUnchanged(t *testing.T) {
	fl := &fakeLayout{} // zero value: nothing ever persisted
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, map[string]any{
		"version": float64(2),
		"wide":    []any{"top-consumers", "events"},
		"narrow":  []any{"storage"},
		"hidden":  []any{},
		"ratio":   overviewRatioDefault,
		"sizes":   map[string]any{},
	}, got)
}

// TestOverviewLayoutGetMergesStoredDocument is the forward-compat rule
// as seen from the wire: a document written by some other build comes
// back usable, never as-is.
func TestOverviewLayoutGetMergesStoredDocument(t *testing.T) {
	fl := &fakeLayout{layout: OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"gone-in-this-build", "events"},
		Narrow:  []string{},
		Hidden:  []string{},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []string{"events", "top-consumers"}, body.Wide)
	require.Equal(t, []string{"storage"}, body.Narrow)
	require.Empty(t, body.Hidden)
	require.Empty(t, fl.setCalls, "a GET never writes the merged document back")
}

func TestOverviewLayoutGetPropagatesGetError(t *testing.T) {
	fl := &fakeLayout{getErr: fmt.Errorf("corrupt blob")}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- PUT ------------------------------------------------------------------

func TestOverviewLayoutPutRoundtripsThroughGet(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":1,"wide":["events"],"narrow":["storage","top-consumers"],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fl.setCalls, 1)

	getResp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var body OverviewLayout
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&body))
	require.Equal(t, OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events"},
		Narrow:  []string{"storage", "top-consumers"},
		Hidden:  []string{},
		Ratio:   overviewRatioDefault,
		Sizes:   map[string]string{},
	}, body)
}

func TestOverviewLayoutPutRoundtripsAHiddenModule(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":1,"wide":["top-consumers"],"narrow":["storage"],"hidden":["events"]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []string{"events"}, body.Hidden)
	require.Equal(t, []string{"top-consumers"}, body.Wide)
}

// TestOverviewLayoutPutStoresTheMergedDocument pins that the merge runs
// on the way IN as well as out: a client that omits a module it doesn't
// know about must not silently drop it from the saved document.
func TestOverviewLayoutPutStoresTheMergedDocument(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":1,"wide":["events"],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, fl.setCalls, 1)
	require.Equal(t, OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events", "top-consumers"},
		Narrow:  []string{"storage"},
		Hidden:  []string{},
		Ratio:   overviewRatioDefault,
		Sizes:   map[string]string{},
	}, fl.setCalls[0])
}

// TestOverviewLayoutPutReplacesEntireDocument pins the whole-document-
// replace contract (LayoutIface.Set's own doc), the same one
// /api/groups uses: there is no per-module route, only a full replace.
func TestOverviewLayoutPutReplacesEntireDocument(t *testing.T) {
	fl := &fakeLayout{layout: OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"storage", "events", "top-consumers"},
		Narrow:  []string{},
		Hidden:  []string{},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":1,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, defaultOverviewLayout(), fl.layout)
}

func TestOverviewLayoutPutRejectsUnknownModuleID(t *testing.T) {
	for _, body := range []string{
		`{"version":1,"wide":["nope"],"narrow":[],"hidden":[]}`,
		`{"version":1,"wide":[],"narrow":["nope"],"hidden":[]}`,
		`{"version":1,"wide":[],"narrow":[],"hidden":["nope"]}`,
	} {
		t.Run(body, func(t *testing.T) {
			fl := &fakeLayout{}
			s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := putSettings(t, ts.URL+"/api/layout/overview", body)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, fl.setCalls)
		})
	}
}

func TestOverviewLayoutPutRejectsDuplicateID(t *testing.T) {
	for name, body := range map[string]string{
		"twice in one column":    `{"version":1,"wide":["events","events"],"narrow":[],"hidden":[]}`,
		"in both columns":        `{"version":1,"wide":["events"],"narrow":["events"],"hidden":[]}`,
		"in a column and hidden": `{"version":1,"wide":["events"],"narrow":[],"hidden":["events"]}`,
		"twice in hidden":        `{"version":1,"wide":[],"narrow":[],"hidden":["events","events"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			fl := &fakeLayout{}
			s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := putSettings(t, ts.URL+"/api/layout/overview", body)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, fl.setCalls)
		})
	}
}

// TestOverviewLayoutPutRejectsAFutureVersion: a document this build
// cannot interpret must be refused outright rather than silently
// re-encoded into whatever shape this build happens to understand. Only
// NEWER is refused -- see the v1 case right below.
func TestOverviewLayoutPutRejectsAFutureVersion(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":99,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fl.setCalls)
}

// TestOverviewLayoutPutAcceptsAV1Document is the cached-bundle case seen
// from the wire: a browser still running the v1 SPA PUTs a v1 document at
// a v2 binary, and it must be accepted and migrated -- not 400'd, which
// would leave that tab unable to save anything at all until it reloaded.
func TestOverviewLayoutPutAcceptsAV1Document(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":1,"wide":["events","top-consumers"],"narrow":["storage"],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, overviewLayoutVersion, body.Version, "the answer is a v2 document")
	require.Equal(t, []string{"events", "top-consumers"}, body.Wide, "the v1 arrangement survives the migration")
	require.InDelta(t, overviewRatioDefault, body.Ratio, 1e-9)
	require.Empty(t, body.Sizes)

	require.Len(t, fl.setCalls, 1)
	require.Equal(t, overviewLayoutVersion, fl.setCalls[0].Version, "and what got STORED is a v2 document too")
}

// TestOverviewLayoutGetMigratesAStoredV1Document is the other direction:
// a config DB written by the shipped v1 build, read by this one.
func TestOverviewLayoutGetMigratesAStoredV1Document(t *testing.T) {
	fl := &fakeLayout{layout: OverviewLayout{
		Version: 1,
		Wide:    []string{"events", "top-consumers"},
		Narrow:  []string{},
		Hidden:  []string{"storage"},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, overviewLayoutVersion, body.Version)
	require.Equal(t, []string{"events", "top-consumers"}, body.Wide)
	require.Equal(t, []string{"storage"}, body.Hidden, "a hidden module stays hidden across the migration")
	require.InDelta(t, overviewRatioDefault, body.Ratio, 1e-9)
	require.Empty(t, body.Sizes)
	require.Empty(t, fl.setCalls, "a GET still never writes the migrated document back")
}

// --- ratio + sizes over the wire ------------------------------------------

func TestOverviewLayoutPutRoundtripsRatioAndSizes(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":2,"wide":["top-consumers","events"],"narrow":["storage"],"hidden":[],`+
			`"ratio":0.72,"sizes":{"events":"tall","top-consumers":"compact"}}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	getResp, err := http.Get(ts.URL + "/api/layout/overview")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var body OverviewLayout
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&body))
	require.InDelta(t, 0.72, body.Ratio, 1e-9)
	require.Equal(t, map[string]string{"events": "tall", "top-consumers": "compact"}, body.Sizes)
}

// TestOverviewLayoutPutRejectsAnOutOfRangeRatio takes the ids' own
// posture: a caller sending a number outside the designed range is a bug
// worth naming, not a value worth quietly repairing (which is what the
// merge does for a blob arriving from storage instead).
func TestOverviewLayoutPutRejectsAnOutOfRangeRatio(t *testing.T) {
	for name, body := range map[string]string{
		"below the minimum": `{"version":2,"wide":[],"narrow":[],"hidden":[],"ratio":0.4}`,
		"above the maximum": `{"version":2,"wide":[],"narrow":[],"hidden":[],"ratio":0.9}`,
		"negative":          `{"version":2,"wide":[],"narrow":[],"hidden":[],"ratio":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			fl := &fakeLayout{}
			s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := putSettings(t, ts.URL+"/api/layout/overview", body)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, fl.setCalls)
		})
	}
}

// TestOverviewLayoutPutAcceptsAnOmittedRatio: 0 is absent, not
// out-of-range -- the case a v1 client and a hand-rolled curl both hit.
func TestOverviewLayoutPutAcceptsAnOmittedRatio(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":2,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fl.setCalls, 1)
	require.InDelta(t, overviewRatioDefault, fl.setCalls[0].Ratio, 1e-9)
}

func TestOverviewLayoutPutRejectsAnUnknownSize(t *testing.T) {
	for name, body := range map[string]string{
		"an unrecognized step": `{"version":2,"wide":[],"narrow":[],"hidden":[],"sizes":{"events":"enormous"}}`,
		"an unknown module id": `{"version":2,"wide":[],"narrow":[],"hidden":[],"sizes":{"nope":"tall"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			fl := &fakeLayout{}
			s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := putSettings(t, ts.URL+"/api/layout/overview", body)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, fl.setCalls)
		})
	}
}

// TestOverviewLayoutPutDropsASizeForANonResizableModule is the one
// deliberate asymmetry in the size validation (see validateOverviewLayout's
// own doc): storage is a real module, so a client sending one size
// per module isn't wrong -- the merge just has nothing to do with it.
func TestOverviewLayoutPutDropsASizeForANonResizableModule(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":2,"wide":[],"narrow":[],"hidden":[],"sizes":{"storage":"tall","events":"compact"}}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body OverviewLayout
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, map[string]string{"events": "compact"}, body.Sizes)
}

// TestOverviewLayoutPutResetsToDefaultRatioAndSizes pins the Reset
// control's own wire behaviour: an all-empty document is how the SPA says
// "defaults", and that has to clear a saved ratio and every saved size,
// not just the arrangement.
func TestOverviewLayoutPutResetsToDefaultRatioAndSizes(t *testing.T) {
	fl := &fakeLayout{layout: OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events", "top-consumers"},
		Narrow:  []string{"storage"},
		Hidden:  []string{},
		Ratio:   0.75,
		Sizes:   map[string]string{"events": overviewSizeTall},
	}}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":2,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, defaultOverviewLayout(), fl.layout)
}

// TestOverviewLayoutPutAcceptsAnOmittedVersion keeps a hand-rolled curl
// (docs/install.md's own scripting audience) from needing to know the
// current schema number to write a layout at all.
func TestOverviewLayoutPutAcceptsAnOmittedVersion(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"wide":["events"],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fl.setCalls, 1)
	require.Equal(t, overviewLayoutVersion, fl.setCalls[0].Version, "the stored document is always stamped with this build's own version")
}

func TestOverviewLayoutPutRejectsUnknownTopLevelField(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":1,"wide":[],"narrow":[],"hidden":[],"extra":1}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fl.setCalls)
}

func TestOverviewLayoutPutMalformedBodyReturns400(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `not json`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fl.setCalls)
}

func TestOverviewLayoutPutNilOptionReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Layout left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":1,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestOverviewLayoutPutPropagatesSetError(t *testing.T) {
	fl := &fakeLayout{setErr: fmt.Errorf("disk full")}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":1,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- read-only / cross-site posture ---------------------------------------
//
// Both pin this route's posture as IDENTICAL to /api/groups', the
// precedent it was modeled on: the cross-site header is required (the
// mux-wide check in gate.go, which every mutating route gets), and
// GANTRY_READ_ONLY is NOT enforced -- a saved layout is config-shaped
// user preference, not a destructive mutation of real containers or
// images. Asserted side by side so the two can never silently diverge.

// The v2 fields ride the same route and therefore take the same posture,
// asserted with a document that actually carries them rather than an
// empty one -- a resize must be exactly as writable under GANTRY_READ_ONLY
// as a reorder is, and exactly as unwritable without the cross-site
// header.
func TestOverviewLayoutPutIsNotGatedByReadOnlyMatchingGroups(t *testing.T) {
	fl := &fakeLayout{}
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl, Groups: fg, ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	layoutResp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":2,"wide":[],"narrow":[],"hidden":[],"ratio":0.7,"sizes":{"events":"tall"}}`)
	defer func() { _ = layoutResp.Body.Close() }()
	groupsResp := putSettings(t, ts.URL+"/api/groups", `{"groups":[]}`)
	defer func() { _ = groupsResp.Body.Close() }()

	require.Equal(t, groupsResp.StatusCode, layoutResp.StatusCode, "layout must answer read-only exactly the way groups does")
	require.Equal(t, http.StatusOK, layoutResp.StatusCode)
	require.Len(t, fl.setCalls, 1, "the write really happened -- this isn't a 200 with nothing behind it")
	require.InDelta(t, 0.7, fl.setCalls[0].Ratio, 1e-9)
	require.Equal(t, map[string]string{"events": overviewSizeTall}, fl.setCalls[0].Sizes)
}

// The cross-site check is mux-wide (gate.go) and never looks at the body,
// so one case covers a resize as completely as it covers a reorder --
// there is deliberately no second, ratio-carrying twin of this.
func TestOverviewLayoutPutRequiresTheCrossSiteHeader(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doReq(t, http.MethodPut, ts.URL+"/api/layout/overview", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Empty(t, fl.setCalls)
}
