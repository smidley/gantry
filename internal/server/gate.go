package server

import (
	"net"
	"net/http"
	"path"

	"github.com/smidley/gantry/internal/auth"
)

// Cross-site request defense for the whole mutating /api surface.
//
// Why SameSite=Lax cookies alone are not enough here: "same site" is
// scheme + registrable domain -- it ignores the PORT. Gantry's normal
// address is http://<unraid-ip>:8380, and an Unraid box runs dozens of
// other containers' web UIs on other ports of that same IP; every one
// of them is SAME-SITE to Gantry, so one compromised (or just
// malicious) container UI could fire authenticated POSTs at the daemon
// and Lax would wave them through. Plain cross-site pages are no better
// off without this check either: an HTML form with enctype=text/plain
// can smuggle a JSON-ish body to any POST route with no preflight, and
// Gantry's decoders never look at Content-Type.
//
// The mechanism -- one, applied uniformly: every mutating request must
// carry a custom header. A custom header can only be attached by
// fetch/XHR, which the browser subjects to a CORS preflight for any
// cross-ORIGIN caller (origin includes the port); Gantry serves no CORS
// headers, so the preflight fails and the request never arrives. HTML
// forms and top-level navigations cannot set custom headers at all.
// Either the SPA-wide `X-Requested-With: gantry` or the destructive
// routes' own established `X-Gantry-Confirm: <resource>` satisfies the
// check -- both are custom headers, so both carry the same
// preflight-forcing property; the confirm header additionally keeps its
// stricter per-resource value check inside requireMutationAllowed.
//
// This is enforced whether or not the password gate is on: with auth
// enabled it is the CSRF defense for cookie-authenticated sessions;
// with auth off it closes the same drive-by hole the confirm header was
// originally added for, for every mutating route rather than only the
// destructive ones. Scripts calling the API directly just add the
// header -- see docs/install.md.
const (
	requestedWithHeader = "X-Requested-With"
	requestedWithValue  = "gantry"
)

// csrfExemptPaths are the mutating paths exempt from the cross-site
// header check. POST /api/healthz is registered purely so fake mode's
// demo webhook target has a same-process endpoint that returns 200
// (see server.go's route comment); it is side-effect-free by contract,
// and a webhook sender is exactly the kind of non-browser client that
// won't send SPA headers.
var csrfExemptPaths = map[string]bool{
	"/api/healthz": true,
}

// isMutatingMethod: everything except the safe methods. PATCH and
// anything exotic count as mutating -- default closed.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func crossSiteHeaderPresent(r *http.Request) bool {
	return r.Header.Get(requestedWithHeader) == requestedWithValue ||
		r.Header.Get(gantryConfirmHeader) != ""
}

// sessionCookieName carries the raw session token. HttpOnly (script
// can never read it), SameSite=Lax (belt to the custom-header braces
// above), Path=/ (the SSE stream and every API route need it), Secure
// whenever the request arrived over TLS -- Gantry itself serves plain
// HTTP, so TLS means a terminating proxy, detected via r.TLS or the
// proxy's own X-Forwarded-Proto. A client spoofing that header only
// makes its OWN cookie stricter, so trusting it here is safe.
//
// It is a SESSION cookie -- deliberately NO Max-Age and NO Expires, so
// the browser drops it when it closes ("until browser closes", the
// owner's chosen session length). The server-side session row remains
// the source of truth for validity and enforces the idle and absolute
// backstops; the cookie merely rides along for the life of the browser.
// A browser that never closes is bounded by those server-side backstops,
// not by the cookie.
const sessionCookieName = "gantry_session"

// authExemptPaths are reachable without a session even while the gate
// is on -- each one for a load-bearing reason:
//   - /api/healthz: the Docker HEALTHCHECK and reverse-proxy checks
//     probe it with no cookie jar; unauthenticated it answers a bare
//     {"status":"ok"} (handleHealthz's split) so nothing sensitive --
//     version, uptime, the sources map's filesystem hint text -- leaks
//     to an unauthenticated LAN scanner.
//   - /api/auth/setup: the first-run bootstrap. It is reachable only
//     while NO credential exists (the manager 409s once one is set), and
//     during that window everything else is 401'd, which is exactly what
//     forces the SPA to render the setup screen.
//   - /api/auth/login: the door itself.
//   - /api/auth/status: the SPA must know whether to render the setup or
//     login screen before it can possibly authenticate. It reveals only
//     the gate's shape (setup vs login vs disabled) -- which those
//     screens reveal anyway -- never the username or any secret.
//   - /api/auth/logout: needs only the cookie it's deleting; letting an
//     expired session "log out" is an idempotent no-op, and gating it
//     would turn logout-after-expiry into a confusing 401.
//
// Everything else under /api -- including paths that don't exist --
// requires a session while the gate is on: the mux's own 404 for an
// unknown API path is only reachable authenticated. The SPA shell and
// static assets are never gated: the app must load to show the setup or
// login screen.
var authExemptPaths = map[string]bool{
	"/api/healthz":     true,
	"/api/auth/setup":  true,
	"/api/auth/login":  true,
	"/api/auth/status": true,
	"/api/auth/logout": true,
}

// gateActive reports whether requests must carry a valid session. Auth
// is mandatory, so the gate is active whenever Gantry owns
// authentication -- ModeAuto -- regardless of whether a credential has
// been set yet: with none set, every non-exempt route 401s and only the
// first-run setup path answers, which is what drives the SPA to the
// setup screen. It stands down only when a reverse proxy owns auth
// (ModeProxy) or the operator explicitly opted the box open
// (ModeNone). Nil Auth means no manager was wired -- a test-only
// convenience that leaves those tests' routes open; the real binary
// always wires one.
func (s *Server) gateActive() bool {
	return s.opts.Auth != nil && s.opts.Auth.Mode() == auth.ModeAuto
}

func sessionTokenFrom(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// requestAuthenticated reports whether r carries a live session. Long-
// lived streams (/api/live, follow logs) are checked once at connect,
// like the rest of the request; a session expiring mid-stream doesn't
// sever it -- the stream carries only what the session was entitled to
// when it opened, and the browser's next reconnect re-checks.
func (s *Server) requestAuthenticated(r *http.Request) bool {
	if s.opts.Auth == nil {
		return false
	}
	token := sessionTokenFrom(r)
	if token == "" {
		return false
	}
	return s.opts.Auth.Authenticate(token)
}

func requestIsTLS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	// No MaxAge and no Expires: net/http writes neither attribute, which
	// is precisely a session cookie -- the browser discards it on close.
	// The server-side row (idle + absolute backstops) is the real clock.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsTLS(r),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsTLS(r),
		MaxAge:   -1,
	})
}

// clientIP is the rate limiter's and audit events' notion of "who":
// the TCP peer, never X-Forwarded-For -- XFF is client-supplied and any
// LAN process could stamp a fresh fake value per request to sidestep
// the per-IP login bucket entirely. In auto mode Gantry is normally hit
// directly, so the peer IS the client; behind a trusted proxy the
// built-in gate is off (GANTRY_AUTH=proxy) and this never matters.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// secureAPI wraps the whole mux (New assigns it to s.root; both
// Handler() and ListenAndServe serve through it) so both checks are
// structurally impossible for a newly added route to forget --
// default-closed for the entire current and future /api surface. The
// SPA shell and its assets are never mutating and never gated: the app
// must be able to load to show the login screen.
//
// Order: cross-site header first (cheapest, and a forged request should
// die as forged regardless of session state), then the session gate.
// The SPA always sends the header, so a legitimate call with an expired
// session still gets the 401 its redirect-to-login flow keys on.
func (s *Server) secureAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Classify on the CLEANED path, never the raw one. isAPIPath is a
		// bare HasPrefix and the two exempt sets below are exact-match
		// maps -- "//api/settings", "/foo/../api/settings", and
		// "/./api/settings" all fail HasPrefix("/api/") outright, which
		// would skip BOTH checks below for a request that, once
		// normalized, is unmistakably a call to a gated route. ServeMux
		// would go on to clean the same path and 307 the caller to the
		// version we just failed to recognize -- so today's safety on
		// those inputs is an accident of the mux's redirect running
		// before any handler does, not a decision this gate actually
		// made, and it disappears entirely for any caller that doesn't
		// replay a 307 (most non-browser API clients default to not).
		// path.Clean makes the gate's own verdict agree with where the
		// mux will end up routing the request, instead of leaning on
		// that redirect happening at all.
		//
		// path.Clean also drops a trailing slash, which the mux's own
		// (unexported) path cleaner does not: "/api/healthz/" therefore
		// classifies here as the exempt "/api/healthz", even though the
		// mux -- trailing slash intact -- never matches that request to
		// handleHealthz; it falls through to the SPA/placeholder's own
		// isAPIPath check and 404s instead. So a trailing slash can
		// widen which requests this gate waves through, but never which
		// handler ends up running one, and nothing in authExemptPaths or
		// csrfExemptPaths is sensitive to wave through in the first
		// place -- see authExemptPaths' own doc. Deliberately not
		// special-cased.
		reqPath := path.Clean(r.URL.Path)
		if isAPIPath(reqPath) {
			if isMutatingMethod(r.Method) && !csrfExemptPaths[reqPath] && !crossSiteHeaderPresent(r) {
				writeError(w, http.StatusForbidden,
					"cross-site request blocked: send the "+requestedWithHeader+": "+requestedWithValue+" header")
				return
			}
			if s.gateActive() && !authExemptPaths[reqPath] && !s.requestAuthenticated(r) {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
