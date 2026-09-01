package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/auth"
	"github.com/stretchr/testify/require"
)

// pathConfusionVariants are three distinct ways to spell /api/settings
// that all fail a bare HasPrefix(path, "/api/") check on the raw,
// un-normalized request path -- a leading double slash, a dot-segment,
// and a "current directory" segment -- even though path.Clean resolves
// every one of them to the real, gated route. secureAPI must classify
// all three as gated on its own; see gate.go's path.Clean comment.
var pathConfusionVariants = []string{
	"//api/settings",
	"/foo/../api/settings",
	"/./api/settings",
}

// noRedirectClient never follows a redirect. The path-confusion cases
// above need to observe secureAPI's own immediate verdict -- if the
// gate ever again classified one of them as outside /api/, the request
// would sail through to the mux, which would 307 it to the clean path
// instead of 401/403ing it directly. A client that dutifully replays
// that redirect would land on the right status anyway and mask the
// exact bug this pins -- see the mux's own cleanPath, which does the
// same normalization one hop too late to matter here.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

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

// TestCrossSiteHeaderRequiredOnPathConfusionVariant pins the CSRF half
// of the L1 path-normalization fix: a dirty path must be blocked for
// missing the cross-site header on the FIRST hop, not waved through
// secureAPI because isAPIPath missed it on the raw path and then
// caught downstream by the mux's own redirect-then-recheck.
func TestCrossSiteHeaderRequiredOnPathConfusionVariant(t *testing.T) {
	s := New(Options{Version: "test", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := noRedirectClient()

	for _, p := range pathConfusionVariants {
		req, err := http.NewRequest(http.MethodPut, ts.URL+p, strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusForbidden, resp.StatusCode,
			"PUT %s with no cross-site header must be blocked directly, not redirected first", p)
	}
}

// --- session-gate matrix ----------------------------------------------------

// gateMatrixRoutes is every registered route class (one representative
// per pattern shape, plus an unknown-API-path probe and the SPA shell).
// The matrix asserts ONLY about the 401 boundary -- whatever else a
// route answers with nil backends (200-with-empty, 404, 400, 503) is
// its own tests' business.
var gateMatrixRoutes = []struct {
	method, path string
	exempt       bool // reachable without a session while the gate is on
}{
	{http.MethodGet, "/api/healthz", true},
	{http.MethodPost, "/api/healthz", true},
	{http.MethodGet, "/api/auth/status", true},
	{http.MethodPost, "/api/auth/login", true},
	{http.MethodPost, "/api/auth/logout", true},

	{http.MethodPost, "/api/auth/password", false},
	{http.MethodPost, "/api/auth/disable", false},
	{http.MethodGet, "/api/version", false},
	{http.MethodGet, "/api/live/snapshot", false},
	{http.MethodGet, "/api/live", false}, // the SSE stream
	{http.MethodGet, "/api/containers", false},
	{http.MethodGet, "/api/containers/demo/logs", false},
	{http.MethodGet, "/api/containers/demo/storage", false},
	{http.MethodGet, "/api/series", false},
	{http.MethodGet, "/api/top", false},
	{http.MethodGet, "/api/events", false},
	{http.MethodGet, "/api/settings", false},
	{http.MethodPut, "/api/settings", false},
	{http.MethodGet, "/api/groups", false},
	{http.MethodPut, "/api/groups", false},
	{http.MethodGet, "/api/images", false},
	{http.MethodPost, "/api/images/remove", false},
	{http.MethodPost, "/api/images/prune", false},
	{http.MethodGet, "/api/containers/maintenance", false},
	{http.MethodPost, "/api/containers/maintenance/remove", false},
	{http.MethodPost, "/api/containers/maintenance/prune", false},
	{http.MethodGet, "/api/alerts", false},
	{http.MethodGet, "/api/alerts/rules", false},
	{http.MethodPut, "/api/alerts/rules", false},
	{http.MethodGet, "/api/alerts/history", false},
	{http.MethodPost, "/api/alerts/silences", false},
	{http.MethodDelete, "/api/alerts/silences/1", false},
	{http.MethodGet, "/api/alerts/webhooks", false},
	{http.MethodPut, "/api/alerts/webhooks", false},
	{http.MethodGet, "/api/acks", false},
	{http.MethodPost, "/api/acks", false},
	{http.MethodDelete, "/api/acks/1", false},
	{http.MethodGet, "/api/insights", false},
	{http.MethodGet, "/api/insights/rules", false},
	{http.MethodPut, "/api/insights/rules", false},
	{http.MethodGet, "/api/insights/history", false},
	{http.MethodGet, "/api/insights/graph", false},
	{http.MethodGet, "/api/insights/1", false},
	{http.MethodPost, "/api/insights/1/dismiss", false},
	{http.MethodGet, "/api/route-from-a-future-version", false}, // unknown API paths are gated too

	// Path confusion (L1 hardening): none of these are exempt -- each
	// spells a real protected route, and the gate must classify every
	// one as gated without any help from the mux's own redirect. See
	// pathConfusionVariants' doc.
	{http.MethodPut, "//api/settings", false},
	{http.MethodPut, "/foo/../api/settings", false},
	{http.MethodPut, "/./api/settings", false},
	// A trailing slash must not de-gate an already-gated route either.
	// HasPrefix never cared about trailing content, so this one holds
	// with or without path.Clean -- pinned anyway since trailing-slash
	// stripping is the specific new behavior Clean introduces here.
	{http.MethodPut, "/api/settings/", false},
}

// gateReq issues one matrix request: cross-site header always present
// (so the CSRF layer never masks the auth result), cookie optional.
// Redirects are never followed: a dirty path the mux would 307 to its
// own canonical form must show up here as that 307, not as whatever
// status a client that replayed the redirect eventually landed on --
// see noRedirectClient's doc.
func gateReq(t *testing.T, base string, method, path, cookie string) int {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set(requestedWithHeader, requestedWithValue)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	resp, err := noRedirectClient().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode
}

func TestGateMatrixLockedWithoutSession(t *testing.T) {
	ts, _, _ := lockedServer(t, Options{})
	for _, rt := range gateMatrixRoutes {
		status := gateReq(t, ts.URL, rt.method, rt.path, "")
		if rt.exempt {
			require.NotEqual(t, http.StatusUnauthorized, status, "%s %s is exempt and must not 401", rt.method, rt.path)
		} else {
			require.Equal(t, http.StatusUnauthorized, status, "%s %s must require a session while locked", rt.method, rt.path)
		}
	}
	// The SPA shell must load unauthenticated -- it renders the login
	// screen.
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGateMatrixLockedWithValidSession(t *testing.T) {
	// A fresh server per row: two matrix routes legitimately mutate auth
	// state when they succeed (POST /api/auth/password wipes every other
	// session, POST /api/auth/disable turns the gate off), which would
	// otherwise poison the rows after them.
	for _, rt := range gateMatrixRoutes {
		ts, _, token := lockedServer(t, Options{})
		status := gateReq(t, ts.URL, rt.method, rt.path, token)
		require.NotEqual(t, http.StatusUnauthorized, status, "%s %s must pass with a valid session", rt.method, rt.path)
		ts.Close()
	}
}

func TestGateMatrixLockedWithBogusCookie(t *testing.T) {
	ts, _, _ := lockedServer(t, Options{})
	for _, rt := range gateMatrixRoutes {
		if rt.exempt {
			continue
		}
		status := gateReq(t, ts.URL, rt.method, rt.path, "forged-or-expired-token")
		require.Equal(t, http.StatusUnauthorized, status, "%s %s must reject an invalid session token", rt.method, rt.path)
	}
}

func TestGateMatrixOpenStates(t *testing.T) {
	// Three open states, all equivalent at the gate: no manager wired,
	// manager with no password set, and proxy mode (the reverse proxy
	// authenticates; the built-in gate stands down). Fresh state per row
	// -- see TestGateMatrixLockedWithValidSession: a successful POST
	// /api/auth/password in the no-password state flips the gate on for
	// every row after it.
	build := map[string]func() http.Handler{
		"nil-auth": func() http.Handler {
			return New(Options{Version: "test-1", Started: time.Now()}).Handler()
		},
		"no-password": func() http.Handler {
			return New(Options{Version: "test-1", Started: time.Now(), Auth: newFakeAuth()}).Handler()
		},
		"proxy-mode": func() http.Handler {
			fa := newFakeAuth()
			fa.mode = auth.ModeProxy
			fa.passwordSet = true
			return New(Options{Version: "test-1", Started: time.Now(), Auth: fa}).Handler()
		},
	}
	for name, mk := range build {
		for _, rt := range gateMatrixRoutes {
			ts := httptest.NewServer(mk())
			status := gateReq(t, ts.URL, rt.method, rt.path, "")
			require.NotEqual(t, http.StatusUnauthorized, status, "[%s] %s %s must not 401 while the gate is off", name, rt.method, rt.path)
			ts.Close()
		}
	}
}
