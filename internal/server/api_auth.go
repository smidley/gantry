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
	// Mode is auth.ModeAuto, auth.ModeProxy, or auth.ModeNone.
	Mode() string
	// CredentialSet reports whether a username+password credential is
	// configured -- with Mode, this decides the setup-vs-login state. The
	// hash itself never crosses this boundary.
	CredentialSet() bool
	// Username is the configured username ("" when none). Not a secret;
	// the status endpoint returns it only to an authenticated caller.
	Username() string
	// EnvManaged reports whether the credential came from
	// GANTRY_USERNAME/GANTRY_PASSWORD at boot -- the Settings UI's "a
	// template edit will overwrite in-app changes" warning.
	EnvManaged() bool
	// Setup is the one-shot first-run bootstrap: creates the initial
	// credential (only while none exists) and returns a fresh token.
	Setup(ip, username, password string) (string, error)
	// Login verifies one attempt (rate-limited per ip) and returns a
	// fresh session token.
	Login(ip, username, password string) (string, error)
	// Authenticate reports whether token names a live session, sliding
	// its expiry.
	Authenticate(token string) bool
	// Logout deletes token's session; idempotent.
	Logout(token string)
	// UpdateCredential changes the username and/or password (current
	// required, rate-limited), wiping all sessions and returning a fresh
	// token for the caller. A blank newPassword is a username-only change.
	UpdateCredential(ip, current, newUsername, newPassword string) (string, error)
}

// authMaxRequestBytes caps every /api/auth body -- a password is at
// most 256 chars and a username 64 by policy, so 4KB is generous, and
// nothing on these routes should ever stream.
const authMaxRequestBytes = 4096

// Auth states the SPA switches on. The server is authoritative -- it
// already knows the mode, whether a credential exists, and whether this
// request is authenticated, so it hands the client a single verdict
// rather than making it re-derive one.
const (
	authStateSetup    = "setup"    // ModeAuto, no credential yet -> first-run setup screen
	authStateLogin    = "login"    // ModeAuto, credential set, not authenticated -> login screen
	authStateAuthed   = "authed"   // ModeAuto, authenticated -> the app
	authStateDisabled = "disabled" // ModeProxy/ModeNone (or no manager) -> the app, no gate
)

type authStatusResponse struct {
	Mode          string `json:"mode"`
	State         string `json:"state"`
	Username      string `json:"username,omitempty"`
	EnvManaged    bool   `json:"env_managed"`
	Authenticated bool   `json:"authenticated"`
}

// handleAuthStatus serves GET /api/auth/status -- the SPA's boot
// question ("setup screen, login screen, or the app?") and the Settings
// access card's state. Reachable unauthenticated by design
// (authExemptPaths); it carries no secrets -- the username is returned
// only to an authenticated caller, everything else is just the gate's
// shape.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	// Nil manager is a test-only convenience meaning "no gate" -- report
	// it as the open, disabled state.
	resp := authStatusResponse{Mode: auth.ModeAuto, State: authStateDisabled}
	if s.opts.Auth != nil {
		a := s.opts.Auth
		resp.Mode = a.Mode()
		resp.EnvManaged = a.EnvManaged()
		authed := s.requestAuthenticated(r)
		resp.Authenticated = authed
		switch a.Mode() {
		case auth.ModeProxy, auth.ModeNone:
			resp.State = authStateDisabled
		default: // ModeAuto -- Gantry owns the gate
			switch {
			case !a.CredentialSet():
				resp.State = authStateSetup
			case authed:
				resp.State = authStateAuthed
				resp.Username = a.Username()
			default:
				resp.State = authStateLogin
			}
		}
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

// requireAuthManaged handles the states in which Gantry's own credential
// routes have nothing to do: no manager wired (tests; 404, the
// Settings-PUT convention for a write with nowhere to go), proxy mode
// (the reverse proxy owns authentication), and none mode (the operator
// explicitly opted the box open). The latter two answer 409 so the inert
// routes can't be mistaken for a working gate.
func (s *Server) requireAuthManaged(w http.ResponseWriter) bool {
	if s.opts.Auth == nil {
		writeError(w, http.StatusNotFound, "authentication unavailable")
		return false
	}
	switch s.opts.Auth.Mode() {
	case auth.ModeProxy:
		writeError(w, http.StatusConflict, "authentication is handled by the reverse proxy (GANTRY_AUTH=proxy)")
		return false
	case auth.ModeNone:
		writeError(w, http.StatusConflict, "authentication is disabled (GANTRY_AUTH=none)")
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
	case errors.Is(err, auth.ErrNoCredential), errors.Is(err, auth.ErrCredentialExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrUsernameEmpty), errors.Is(err, auth.ErrUsernameTooLong),
		errors.Is(err, auth.ErrUsernameInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleAuthSetup serves POST /api/auth/setup: the first-run bootstrap.
// Username + password in, session cookie out. Reachable without a
// session (authExemptPaths) because there's nothing yet to authenticate
// against -- exactly as exposed as every other route on a never-
// configured box, and the mux-wide cross-site header still shuts out
// drive-by pages. Once a credential exists the manager answers 409
// (ErrCredentialExists), so this is genuinely one-shot.
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthManaged(w) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	token, err := s.opts.Auth.Setup(clientIP(r), body.Username, body.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleAuthLogin serves POST /api/auth/login: username + password in,
// session cookie out. 401 for a wrong credential, 429 (with Retry-After)
// once the per-IP or global bucket is dry, 409 when there is nothing to
// log in to yet (no credential set -> setup first, or proxy/none mode).
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthManaged(w) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	token, err := s.opts.Auth.Login(clientIP(r), body.Username, body.Password)
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

// handleAuthCredential serves POST /api/auth/credential -- change the
// username and/or password. The mux-wide gate already required a live
// session to reach this handler (it is NOT in authExemptPaths), and
// UpdateCredential itself re-verifies current_password (a stolen open
// tab must not be enough to rotate the credential quietly). A blank
// new_password is a username-only change. On success the response
// carries a fresh cookie -- UpdateCredential wiped every session,
// including the caller's own.
//
// Deliberately NOT gated by ReadOnly: GANTRY_READ_ONLY is the docker
// write-path kill switch, and being unable to change the login on a
// read-only box would be a foot-gun. The two switches are orthogonal.
func (s *Server) handleAuthCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuthManaged(w) {
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeAuthBody(w, r, &body) {
		return
	}
	token, err := s.opts.Auth.UpdateCredential(clientIP(r), body.CurrentPassword, body.NewUsername, body.NewPassword)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.setSessionCookie(w, r, token)
	writeJSON(w, map[string]bool{"ok": true})
}
