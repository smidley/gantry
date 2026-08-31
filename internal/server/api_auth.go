package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/smidley/gantry/internal/auth"
)

// AuthIface is the slice of *auth.Manager the server needs (main wires
// the manager directly; tests wire a fake) -- the SettingsIface/
// AlertsIface convention. Error VALUES cross this boundary as
// internal/auth's sentinels (ErrRateLimited and friends), which is why
// this package imports auth for errors.Is mapping; auth never imports
// server, so the dependency stays one-way.
type AuthIface interface {
	// Mode is auth.ModeAuto or auth.ModeProxy.
	Mode() string
	// PasswordSet reports whether a password is configured -- with Mode,
	// this decides gateActive. The hash itself never crosses this
	// boundary: GETs expose configured-or-not only, the webhook-secret
	// discipline.
	PasswordSet() bool
	// EnvManaged reports whether the password came from GANTRY_PASSWORD
	// at boot -- the Settings UI's "a template edit will overwrite
	// in-app changes" warning.
	EnvManaged() bool
	// Login verifies one attempt (rate-limited per ip) and returns a
	// fresh session token.
	Login(ip, password string) (string, error)
	// Authenticate reports whether token names a live session, sliding
	// its expiry.
	Authenticate(token string) bool
	// Logout deletes token's session; idempotent.
	Logout(token string)
	// SetPassword sets (current ignored while none is configured) or
	// changes (current required, rate-limited) the password, wiping all
	// sessions and returning a fresh token for the caller.
	SetPassword(ip, current, newPassword string) (string, error)
	// Disable turns the gate off; requires the current password.
	Disable(ip, current string) error
}

// authMaxRequestBytes caps every /api/auth body -- a password is at
// most 256 chars by policy, so 4KB is generous, and nothing on these
// routes should ever stream.
const authMaxRequestBytes = 4096

type authStatusResponse struct {
	Mode          string `json:"mode"`
	PasswordSet   bool   `json:"password_set"`
	EnvManaged    bool   `json:"env_managed"`
	Authenticated bool   `json:"authenticated"`
}

// handleAuthStatus serves GET /api/auth/status -- the SPA's boot
// question ("do I show the login screen?") and the Settings access
// card's state. Reachable unauthenticated by design (authExemptPaths);
// it carries no secrets, only the gate's shape.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	resp := authStatusResponse{Mode: auth.ModeAuto}
	if s.opts.Auth != nil {
		resp.Mode = s.opts.Auth.Mode()
		resp.PasswordSet = s.opts.Auth.PasswordSet()
		resp.EnvManaged = s.opts.Auth.EnvManaged()
		resp.Authenticated = s.requestAuthenticated(r)
	}
	writeJSON(w, resp)
}

// decodeAuthBody decodes one size-capped, unknown-field-rejecting auth
// request body, writing the 400/413 itself on failure.
func decodeAuthBody(w http.ResponseWriter, r *http.Request, into any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, authMaxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeDecodeError(w, err)
		return false
	}
	return true
}

// requireAuthConfigured handles the two states every auth mutation
// shares: no manager wired (tests; 404, the Settings-PUT convention for
// a write with nowhere to go) and proxy mode (409: the reverse proxy
// owns authentication; Gantry's own password lifecycle is deliberately
// inert rather than half-alive underneath it).
func (s *Server) requireAuthConfigured(w http.ResponseWriter) bool {
	if s.opts.Auth == nil {
		writeError(w, http.StatusNotFound, "authentication unavailable")
		return false
	}
	if s.opts.Auth.Mode() == auth.ModeProxy {
		writeError(w, http.StatusConflict, "authentication is handled by the reverse proxy (GANTRY_AUTH=proxy)")
		return false
	}
	return true
}

// writeAuthError maps internal/auth's sentinels onto statuses. The
// bodies are the sentinels' own user-facing messages; none ever carries
// password material.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, auth.ErrBadCredentials):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, auth.ErrNoPassword):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleAuthLogin serves POST /api/auth/login: password in, session
// cookie out. 401 for a wrong password, 429 (with Retry-After) once the
// per-IP or global bucket is dry, 409 when there is nothing to log in
// to (no password set, or proxy mode).
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthConfigured(w) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	token, err := s.opts.Auth.Login(clientIP(r), body.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleAuthLogout serves POST /api/auth/logout: deletes the request's
// session (if any) and expires the cookie. Always 204 -- logging out an
// already-dead session is success, not failure.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.opts.Auth != nil {
		if token := sessionTokenFrom(r); token != "" {
			s.opts.Auth.Logout(token)
		}
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthPassword serves POST /api/auth/password -- set or change.
// While a password is set, the mux-wide gate already required a live
// session to reach this handler AND SetPassword itself re-verifies
// current_password (a stolen open tab must not be enough to rotate the
// password quietly). While none is set, this is the bootstrap: it must
// be callable openly, which is exactly as exposed as every other route
// already is on a zero-config install. On success the response carries
// a fresh cookie -- SetPassword wiped every session including the
// caller's own.
//
// Deliberately NOT gated by ReadOnly: GANTRY_READ_ONLY is the docker
// write-path kill switch, and being unable to SECURE a read-only box
// would invert its purpose. The two switches are orthogonal.
func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthConfigured(w) {
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	token, err := s.opts.Auth.SetPassword(clientIP(r), body.CurrentPassword, body.NewPassword)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleAuthDisable serves POST /api/auth/disable -- the ONLY way to
// turn the gate off (removing GANTRY_PASSWORD does not; see
// auth.Manager.EnsureEnvPassword). Session-gated by the mux wrapper
// like every non-exempt route, and the current password is required on
// top, same reasoning as handleAuthPassword.
func (s *Server) handleAuthDisable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthConfigured(w) {
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	if err := s.opts.Auth.Disable(clientIP(r), body.CurrentPassword); err != nil {
		writeAuthError(w, err)
		return
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}
