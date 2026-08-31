package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// csrfMatrix is one request per mutating route family plus control
// GETs -- the cross-site header check is enforced by one wrapper over
// the whole mux, so one representative per shape is enough to pin the
// wrapper's reach without re-testing the mux.
var csrfMutatingRoutes = []struct {
	method, path string
}{
	{http.MethodPut, "/api/settings"},
	{http.MethodPut, "/api/groups"},
	{http.MethodPost, "/api/images/remove"},
	{http.MethodPost, "/api/images/prune"},
	{http.MethodPost, "/api/containers/maintenance/remove"},
	{http.MethodPost, "/api/containers/maintenance/prune"},
	{http.MethodPut, "/api/alerts/rules"},
	{http.MethodPost, "/api/alerts/silences"},
	{http.MethodDelete, "/api/alerts/silences/1"},
	{http.MethodPut, "/api/alerts/webhooks"},
	{http.MethodPost, "/api/acks"},
	{http.MethodDelete, "/api/acks/1"},
	{http.MethodPut, "/api/insights/rules"},
	{http.MethodPost, "/api/insights/1/dismiss"},
	{http.MethodPost, "/api/nonexistent-future-route"}, // default-closed for routes that don't exist yet
}

func doReq(t *testing.T, method, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("{}"))
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp
}

func TestCrossSiteHeaderRequiredOnEveryMutatingAPIRoute(t *testing.T) {
	s := New(Options{Version: "test", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, rt := range csrfMutatingRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// No custom header at all: blocked before any handler runs.
			resp := doReq(t, rt.method, ts.URL+rt.path, nil)
			require.Equal(t, http.StatusForbidden, resp.StatusCode, "bare mutating request must be blocked")

			// The SPA-wide header passes the check (whatever status the
			// route itself then answers -- unwired handlers 404, decode
			// failures 400 -- it must not be the CSRF 403).
			resp = doReq(t, rt.method, ts.URL+rt.path, map[string]string{requestedWithHeader: requestedWithValue})
			require.NotEqual(t, http.StatusForbidden, resp.StatusCode, "X-Requested-With: gantry must satisfy the check")

			// The established confirm header satisfies it too -- both
			// are custom headers, same preflight-forcing property.
			resp = doReq(t, rt.method, ts.URL+rt.path, map[string]string{gantryConfirmHeader: "images"})
			require.NotEqual(t, http.StatusForbidden, resp.StatusCode, "X-Gantry-Confirm must satisfy the check")

			// A wrong X-Requested-With value is as absent -- the check
			// is for OUR marker, not the header's mere existence.
			resp = doReq(t, rt.method, ts.URL+rt.path, map[string]string{requestedWithHeader: "XMLHttpRequest"})
			require.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}

func TestCrossSiteHeaderNotRequiredOnSafeMethodsOrExemptPaths(t *testing.T) {
	s := New(Options{Version: "test", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/version", "/api/settings", "/api/events", "/api/healthz"} {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s must not need the header", path)
	}

	// POST /api/healthz is the fake-mode demo webhook target -- a
	// non-browser caller with no SPA headers, exempt by design.
	resp := doReq(t, http.MethodPost, ts.URL+"/api/healthz", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The SPA shell is not an API path; no method of it is checked.
	resp2, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	require.NoError(t, resp2.Body.Close())
	require.Equal(t, http.StatusOK, resp2.StatusCode)
}
