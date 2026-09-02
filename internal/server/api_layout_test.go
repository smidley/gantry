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
		Narrow:  []string{"metrics-rail"},
		Hidden:  []string{"another-ghost"},
	})
	require.Equal(t, []string{"events", "top-consumers"}, merged.Wide)
	require.Equal(t, []string{"metrics-rail"}, merged.Narrow)
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
	require.Equal(t, []string{"metrics-rail"}, merged.Narrow, "the missing narrow module lands in the narrow column, not the wide one")
	require.Empty(t, merged.Hidden)
}

// TestMergeOverviewLayoutKeepsAHiddenModuleHidden pins that a module the
// user hid is NOT re-placed into a column by the missing-id rule: it is
// already accounted for, just in the hidden list.
func TestMergeOverviewLayoutKeepsAHiddenModuleHidden(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"top-consumers"},
		Narrow:  []string{"metrics-rail"},
		Hidden:  []string{"events"},
	})
	require.Equal(t, []string{"top-consumers"}, merged.Wide)
	require.Equal(t, []string{"metrics-rail"}, merged.Narrow)
	require.Equal(t, []string{"events"}, merged.Hidden)
}

func TestMergeOverviewLayoutDeduplicatesFirstOccurrenceWins(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"events", "events", "metrics-rail"},
		Narrow:  []string{"metrics-rail", "top-consumers"},
		Hidden:  []string{"events"},
	})
	require.Equal(t, []string{"events", "metrics-rail"}, merged.Wide)
	require.Equal(t, []string{"top-consumers"}, merged.Narrow)
	require.Empty(t, merged.Hidden)
}

// TestMergeOverviewLayoutPreservesUserOrder is the whole point of saving
// a layout at all -- a fully-populated document comes back byte-for-byte
// as it went in, columns swapped and all.
func TestMergeOverviewLayoutPreservesUserOrder(t *testing.T) {
	stored := OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"metrics-rail", "events"},
		Narrow:  []string{"top-consumers"},
		Hidden:  []string{},
	}
	require.Equal(t, stored, mergeOverviewLayout(stored))
}

// TestMergeOverviewLayoutNeverReturnsNilSlices keeps the JSON wire shape
// honest: a nil []string marshals to `null`, which the SPA would have to
// guard on every read. Every list is always a real (if empty) array.
func TestMergeOverviewLayoutNeverReturnsNilSlices(t *testing.T) {
	merged := mergeOverviewLayout(OverviewLayout{})
	require.NotNil(t, merged.Wide)
	require.NotNil(t, merged.Narrow)
	require.NotNil(t, merged.Hidden)
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
	require.Equal(t, []string{"metrics-rail"}, body.Narrow)
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
		`{"version":1,"wide":["events"],"narrow":["metrics-rail","top-consumers"],"hidden":[]}`)
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
		Narrow:  []string{"metrics-rail", "top-consumers"},
		Hidden:  []string{},
	}, body)
}

func TestOverviewLayoutPutRoundtripsAHiddenModule(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/layout/overview",
		`{"version":1,"wide":["top-consumers"],"narrow":["metrics-rail"],"hidden":["events"]}`)
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
		Narrow:  []string{"metrics-rail"},
		Hidden:  []string{},
	}, fl.setCalls[0])
}

// TestOverviewLayoutPutReplacesEntireDocument pins the whole-document-
// replace contract (LayoutIface.Set's own doc), the same one
// /api/groups uses: there is no per-module route, only a full replace.
func TestOverviewLayoutPutReplacesEntireDocument(t *testing.T) {
	fl := &fakeLayout{layout: OverviewLayout{
		Version: overviewLayoutVersion,
		Wide:    []string{"metrics-rail", "events", "top-consumers"},
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
// re-encoded into whatever shape this build happens to understand.
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

func TestOverviewLayoutPutIsNotGatedByReadOnlyMatchingGroups(t *testing.T) {
	fl := &fakeLayout{}
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl, Groups: fg, ReadOnly: true})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	layoutResp := putSettings(t, ts.URL+"/api/layout/overview", `{"version":1,"wide":[],"narrow":[],"hidden":[]}`)
	defer func() { _ = layoutResp.Body.Close() }()
	groupsResp := putSettings(t, ts.URL+"/api/groups", `{"groups":[]}`)
	defer func() { _ = groupsResp.Body.Close() }()

	require.Equal(t, groupsResp.StatusCode, layoutResp.StatusCode, "layout must answer read-only exactly the way groups does")
	require.Equal(t, http.StatusOK, layoutResp.StatusCode)
	require.Len(t, fl.setCalls, 1, "the write really happened -- this isn't a 200 with nothing behind it")
}

func TestOverviewLayoutPutRequiresTheCrossSiteHeader(t *testing.T) {
	fl := &fakeLayout{}
	s := New(Options{Version: "test-1", Started: time.Now(), Layout: fl})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := doReq(t, http.MethodPut, ts.URL+"/api/layout/overview", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Empty(t, fl.setCalls)
}
