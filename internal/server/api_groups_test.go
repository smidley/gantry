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

// fakeGroups is a minimal in-memory GroupsIface -- the test double every
// /api/groups test in this file wires in place of main's real
// store-backed adapter.
type fakeGroups struct {
	groups   []Group
	getErr   error
	setErr   error
	setCalls [][]Group
}

func (f *fakeGroups) Get() ([]Group, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.groups, nil
}

func (f *fakeGroups) Set(groups []Group) error {
	f.setCalls = append(f.setCalls, groups)
	if f.setErr != nil {
		return f.setErr
	}
	f.groups = groups
	return nil
}

func TestGroupsGetReturnsCurrentGroups(t *testing.T) {
	fg := &fakeGroups{groups: []Group{{Name: "media", Members: []string{"jellyfin", "sonarr"}}}}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/groups")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body groupsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, []Group{{Name: "media", Members: []string{"jellyfin", "sonarr"}}}, body.Groups)
}

func TestGroupsGetNilOptionReturnsEmptyNotPanic(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Groups left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/groups")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body groupsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body.Groups)
}

func TestGroupsGetPropagatesGetError(t *testing.T) {
	fg := &fakeGroups{getErr: fmt.Errorf("corrupt blob")}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/groups")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestGroupsPutRoundtripsThroughGet(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[{"name":"media","members":["jellyfin","sonarr"]},{"name":"empty","members":[]}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, fg.setCalls, 1)

	getResp, err := http.Get(ts.URL + "/api/groups")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var body groupsResponse
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&body))
	require.Equal(t, []Group{
		{Name: "media", Members: []string{"jellyfin", "sonarr"}},
		{Name: "empty", Members: []string{}},
	}, body.Groups)
}

// TestGroupsPutReplacesEntireList pins the whole-document-replace
// contract (GroupsIface.Set's own doc): a PUT that omits a
// previously-saved group deletes it -- there's no per-group
// create/rename/delete route, only a full-list replace.
func TestGroupsPutReplacesEntireList(t *testing.T) {
	fg := &fakeGroups{groups: []Group{{Name: "old", Members: []string{"a"}}}}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[{"name":"new","members":["b"]}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, []Group{{Name: "new", Members: []string{"b"}}}, fg.groups)
}

func TestGroupsPutRejectsEmptyName(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[{"name":"","members":["a"]}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fg.setCalls)
}

func TestGroupsPutRejectsDuplicateName(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[{"name":"media","members":["a"]},{"name":"media","members":["b"]}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fg.setCalls)
}

// TestGroupsPutAllowsSameNameDifferentCase pins that name comparison is
// exact, byte-for-byte -- "Media" and "media" are two different,
// both-valid names, the same case-sensitivity every other name
// comparison in this app (container names included) uses.
func TestGroupsPutAllowsSameNameDifferentCase(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[{"name":"media","members":["a"]},{"name":"Media","members":["b"]}]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGroupsPutRejectsTooManyGroups(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	groups := make([]Group, maxGroups+1)
	for i := range groups {
		groups[i] = Group{Name: fmt.Sprintf("g%d", i), Members: []string{"a"}}
	}
	body, err := json.Marshal(groupsResponse{Groups: groups})
	require.NoError(t, err)

	resp := putSettings(t, ts.URL+"/api/groups", string(body))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fg.setCalls)
}

func TestGroupsPutAcceptsExactlyMaxGroups(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	groups := make([]Group, maxGroups)
	for i := range groups {
		groups[i] = Group{Name: fmt.Sprintf("g%d", i), Members: []string{"a"}}
	}
	body, err := json.Marshal(groupsResponse{Groups: groups})
	require.NoError(t, err)

	resp := putSettings(t, ts.URL+"/api/groups", string(body))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the documented max group count must be accepted, not rejected")
}

func TestGroupsPutRejectsTooManyMembers(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	members := make([]string, maxGroupMembers+1)
	for i := range members {
		members[i] = fmt.Sprintf("c%d", i)
	}
	body, err := json.Marshal(groupsResponse{Groups: []Group{{Name: "big", Members: members}}})
	require.NoError(t, err)

	resp := putSettings(t, ts.URL+"/api/groups", string(body))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fg.setCalls)
}

func TestGroupsPutAcceptsExactlyMaxMembers(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	members := make([]string, maxGroupMembers)
	for i := range members {
		members[i] = fmt.Sprintf("c%d", i)
	}
	body, err := json.Marshal(groupsResponse{Groups: []Group{{Name: "big", Members: members}}})
	require.NoError(t, err)

	resp := putSettings(t, ts.URL+"/api/groups", string(body))
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the documented max member count must be accepted, not rejected")
}

func TestGroupsPutRejectsUnknownTopLevelField(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[{"name":"media","members":["a"]}],"extra":1}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fg.setCalls)
}

func TestGroupsPutMalformedBodyReturns400(t *testing.T) {
	fg := &fakeGroups{}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `not json`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fg.setCalls)
}

func TestGroupsPutNilOptionReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Groups left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestGroupsPutPropagatesSetError proves a Set failure surfaces as a
// 500 rather than a false-success 200 -- the same "don't swallow the
// backing store's own error" contract every other handler in this
// package follows for its optional closures (see Settings' own
// identically-named test).
func TestGroupsPutPropagatesSetError(t *testing.T) {
	fg := &fakeGroups{setErr: fmt.Errorf("disk full")}
	s := New(Options{Version: "test-1", Started: time.Now(), Groups: fg})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/groups", `{"groups":[]}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}
