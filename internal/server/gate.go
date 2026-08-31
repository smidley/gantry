package server

import (
	"net/http"
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

// secureAPI wraps the whole mux (New assigns it to s.root; both
// Handler() and ListenAndServe serve through it) so the check is
// structurally impossible for a newly added route to forget --
// default-closed for the entire current and future /api surface. The
// SPA shell and its assets are never mutating and never checked.
func (s *Server) secureAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			if isMutatingMethod(r.Method) && !csrfExemptPaths[r.URL.Path] && !crossSiteHeaderPresent(r) {
				writeError(w, http.StatusForbidden,
					"cross-site request blocked: send the "+requestedWithHeader+": "+requestedWithValue+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
