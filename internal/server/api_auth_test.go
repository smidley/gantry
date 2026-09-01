package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/auth"
	"github.com/stretchr/testify/require"
)

// fakeAuth is a minimal in-memory AuthIface double -- the fakeAcks
// convention. It carries just enough realistic behavior (setup is
// one-shot, login/credential need a credential) that the gate matrix and
// handler tests exercise real status paths; errors are injected via the
// err fields using internal/auth's own sentinels, exactly what the real
// manager returns across this boundary.
type fakeAuth struct {
	mode          string
	credentialSet bool
	username      string
	envManaged    bool

	valid map[string]bool // token -> live session

	setupToken  string
	setupErr    error
	loginToken  string
	loginErr    error
	updateToken string
	updateErr   error

	lastSetupIP, lastSetupUser, lastSetupPass string
	lastLoginIP, lastLoginUser, lastLoginPass string
	lastUpdateIP, lastUpdateCur               string
	lastUpdateUser, lastUpdatePass            string
	loggedOut                                 []string
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{mode: auth.ModeAuto, valid: map[string]bool{}}
}

func (f *fakeAuth) Mode() string        { return f.mode }
func (f *fakeAuth) CredentialSet() bool { return f.credentialSet }
func (f *fakeAuth) Username() string    { return f.username }
func (f *fakeAuth) EnvManaged() bool    { return f.envManaged }

func (f *fakeAuth) Setup(ip, username, password string) (string, error) {
	f.lastSetupIP, f.lastSetupUser, f.lastSetupPass = ip, username, password
	if f.setupErr != nil {
		return "", f.setupErr
	}
	if f.credentialSet {
		return "", auth.ErrCredentialExists
	}
	f.valid = map[string]bool{f.setupToken: true}
	f.credentialSet = true
	f.username = username
	return f.setupToken, nil
}

func (f *fakeAuth) Login(ip, username, password string) (string, error) {
	f.lastLoginIP, f.lastLoginUser, f.lastLoginPass = ip, username, password
	if f.loginErr != nil {
		return "", f.loginErr
	}
	if !f.credentialSet {
		return "", auth.ErrNoCredential
	}
	f.valid[f.loginToken] = true
	return f.loginToken, nil
}

func (f *fakeAuth) Authenticate(token string) bool { return f.valid[token] }

func (f *fakeAuth) Logout(token string) {
	f.loggedOut = append(f.loggedOut, token)
	delete(f.valid, token)
}

func (f *fakeAuth) UpdateCredential(ip, current, newUsername, newPassword string) (string, error) {
	f.lastUpdateIP, f.lastUpdateCur = ip, current
	f.lastUpdateUser, f.lastUpdatePass = newUsername, newPassword
	if f.updateErr != nil {
		return "", f.updateErr
	}
	if !f.credentialSet {
		return "", auth.ErrNoCredential
	}
	f.valid = map[string]bool{f.updateToken: true}
	if newUsername != "" {
		f.username = newUsername
	}
	return f.updateToken, nil
}

// lockedServer is a server with the gate ON and a credential set: auto
// mode, credential set (username alice), one valid session token.
func lockedServer(t *testing.T, opts Options) (*httptest.Server, *fakeAuth, string) {
	t.Helper()
	fa := newFakeAuth()
	fa.credentialSet = true
	fa.username = "alice"
	const token = "valid-session-token"
	fa.valid[token] = true
	fa.setupToken = "fresh-setup-token"
	fa.loginToken = "fresh-login-token"
	fa.updateToken = "fresh-update-token"
	opts.Auth = fa
	if opts.Version == "" {
		opts.Version = "test-1"
	}
	if opts.Started.IsZero() {
		opts.Started = time.Now()
	}
	s := New(opts)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, fa, token
}

func authReq(t *testing.T, method, url, body, cookie string) *http.Response {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(requestedWithHeader, requestedWithValue)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func sessionCookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

// rawSessionSetCookie returns the raw Set-Cookie header line for the
// session cookie -- the only way to assert an ATTRIBUTE is absent, since
// the parsed http.Cookie can't distinguish "Max-Age omitted" from
// "Max-Age: 0".
func rawSessionSetCookie(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, line := range resp.Header["Set-Cookie"] {
		if strings.HasPrefix(line, sessionCookieName+"=") {
			return line
		}
	}
	return ""
}

func TestAuthLoginSetsSessionCookieWithNoMaxAge(t *testing.T) {
	ts, fa, _ := lockedServer(t, Options{})

	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"username":"alice","password":"pw-goes-here"}`, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "alice", fa.lastLoginUser)
	require.Equal(t, "pw-goes-here", fa.lastLoginPass)
	require.Equal(t, "127.0.0.1", fa.lastLoginIP, "the limiter must see the TCP peer's bare IP, not host:port")

	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c, "login must set the session cookie")
	require.Equal(t, "fresh-login-token", c.Value)
	require.True(t, c.HttpOnly, "script must never be able to read the token")
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, "/", c.Path, "every API route and the SSE stream need the cookie")
	require.False(t, c.Secure, "plain-HTTP LAN requests must not get a Secure cookie the browser would then drop")

	// The owner's chosen session length is "until the browser closes": a
	// SESSION cookie, so the Set-Cookie must carry neither Max-Age nor
	// Expires. The parsed values reflect that, and the raw header proves
	// the attributes are genuinely absent (not just zero).
	require.Zero(t, c.MaxAge, "a session cookie must have no Max-Age")
	require.True(t, c.Expires.IsZero(), "a session cookie must have no Expires")
	raw := rawSessionSetCookie(t, resp)
	require.NotEmpty(t, raw)
	require.NotContains(t, raw, "Max-Age", "the Set-Cookie must not carry a Max-Age attribute at all")
	require.NotContains(t, raw, "Expires", "the Set-Cookie must not carry an Expires attribute at all")
}

func TestAuthLoginSecureCookieBehindTLSTerminatingProxy(t *testing.T) {
	ts, _, _ := lockedServer(t, Options{})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", strings.NewReader(`{"username":"alice","password":"x"}`))
	require.NoError(t, err)
	req.Header.Set(requestedWithHeader, requestedWithValue)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c)
	require.True(t, c.Secure, "TLS detected via the proxy's X-Forwarded-Proto must mark the cookie Secure")
}

func TestAuthLoginErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{auth.ErrBadCredentials, http.StatusUnauthorized},
		{auth.ErrRateLimited, http.StatusTooManyRequests},
		{auth.ErrNoCredential, http.StatusConflict},
	}
	for _, tc := range cases {
		ts, fa, _ := lockedServer(t, Options{})
		fa.loginErr = tc.err
		resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"username":"alice","password":"x"}`, "")
		require.Equal(t, tc.status, resp.StatusCode, "for %v", tc.err)
		require.Nil(t, sessionCookieFrom(t, resp), "a failed login must never set a cookie")
		if tc.err == auth.ErrRateLimited {
			require.Equal(t, "60", resp.Header.Get("Retry-After"))
		}
		var body map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, tc.err.Error(), body["error"])
	}
}

func TestAuthLoginBodyValidation(t *testing.T) {
	ts, _, _ := lockedServer(t, Options{})

	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"username":"a","password":"x","extra":true}`, "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown fields must fail the decode")

	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"username":"a","password":"`+strings.Repeat("a", 8192)+`"}`, "")
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "a body over the auth cap must 413")
}

func TestAuthRoutesUnavailableWithoutManagerOrGateOff(t *testing.T) {
	// Nil manager (tests): 404 -- a write with nowhere to go.
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"username":"a","password":"x"}`, "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Proxy and none both own/own-away authentication: the built-in
	// routes answer 409 so an inert gate can't be mistaken for a working
	// one.
	for _, mode := range []string{auth.ModeProxy, auth.ModeNone} {
		ts2, fa, _ := lockedServer(t, Options{})
		fa.mode = mode
		for _, path := range []string{"/api/auth/login", "/api/auth/setup", "/api/auth/credential"} {
			resp = authReq(t, http.MethodPost, ts2.URL+path, `{"username":"a","password":"x"}`, "valid-session-token")
			require.Equal(t, http.StatusConflict, resp.StatusCode, "%s must 409 in %s mode", path, mode)
		}
	}
}

func TestAuthLogoutDeletesSessionAndExpiresCookie(t *testing.T) {
	ts, fa, token := lockedServer(t, Options{})

	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/logout", "", token)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, []string{token}, fa.loggedOut)
	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c, "logout must expire the cookie")
	require.Less(t, c.MaxAge, 0)
	require.Empty(t, c.Value)

	// Without any cookie: still 204 -- logging out a dead session is
	// success. And it stays reachable while locked (exempt path).
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/logout", "", "")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestAuthStatusShapes(t *testing.T) {
	// Nil manager: a test-only open box -- reported as the disabled state.
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	var st authStatusResponse
	resp := authReq(t, http.MethodGet, ts.URL+"/api/auth/status", "", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.Equal(t, authStatusResponse{Mode: auth.ModeAuto, State: authStateDisabled}, st)

	// Setup-pending: auto mode, no credential yet.
	fa := newFakeAuth()
	ss := New(Options{Version: "test-1", Started: time.Now(), Auth: fa})
	tsSetup := httptest.NewServer(ss.Handler())
	defer tsSetup.Close()
	resp = authReq(t, http.MethodGet, tsSetup.URL+"/api/auth/status", "", "")
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.Equal(t, authStateSetup, st.State)
	require.Empty(t, st.Username, "the username must not leak before setup (there is none)")

	// Locked, no cookie: login state; env_managed surfaced; username
	// withheld from the unauthenticated caller.
	ts2, fa2, token := lockedServer(t, Options{})
	fa2.envManaged = true
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/auth/status", "", "")
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.Equal(t, authStatusResponse{Mode: auth.ModeAuto, State: authStateLogin, EnvManaged: true}, st)
	require.Empty(t, st.Username, "an unauthenticated caller must not be told the username")

	// Locked, valid cookie: authed state, username returned.
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/auth/status", "", token)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.Equal(t, authStateAuthed, st.State)
	require.True(t, st.Authenticated)
	require.Equal(t, "alice", st.Username)

	// Proxy and none: the disabled state either way.
	for _, mode := range []string{auth.ModeProxy, auth.ModeNone} {
		ts3, fa3, _ := lockedServer(t, Options{})
		fa3.mode = mode
		resp = authReq(t, http.MethodGet, ts3.URL+"/api/auth/status", "", "")
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
		require.Equal(t, authStateDisabled, st.State, "mode %s", mode)
	}

	// The response must never leak hash material -- checked on the raw
	// bytes, not a decoded struct that could silently drop a field.
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/auth/status", "", "")
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "argon2")
	require.NotContains(t, string(raw), "hash")
}

func TestAuthSetupOneShotBootstrapThenLoginGate(t *testing.T) {
	// Gate ON but NO credential (setup-pending): setup must be reachable
	// with no session -- the bootstrap. The fake flips credentialSet true.
	fa := newFakeAuth()
	fa.setupToken = "boot-token"
	s := New(Options{Version: "test-1", Started: time.Now(), Auth: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Data routes are gated even with no credential -- forcing the setup
	// screen -- but /api/auth/setup answers.
	resp := authReq(t, http.MethodGet, ts.URL+"/api/live/snapshot", "", "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "everything but setup is 401 while awaiting first-run setup")

	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/setup", `{"username":"alice","password":"a-decent-password"}`, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "alice", fa.lastSetupUser)
	require.Equal(t, "a-decent-password", fa.lastSetupPass)
	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c, "setup must leave the caller logged in")
	require.Equal(t, "boot-token", c.Value)
	require.NotContains(t, rawSessionSetCookie(t, resp), "Max-Age", "the setup cookie is a session cookie too")

	// Credential now set: a SECOND setup is refused (409, one-shot), even
	// with the session the first one issued.
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/setup", `{"username":"mallory","password":"another-one"}`, "boot-token")
	require.Equal(t, http.StatusConflict, resp.StatusCode, "setup is one-shot: once a credential exists it 409s")

	// And the change route is now gated: no session 401s, a session
	// reaches the handler.
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/credential", `{"current_password":"a-decent-password","new_username":"alice","new_password":"another-one"}`, "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/credential", `{"current_password":"a-decent-password","new_username":"alice","new_password":"another-one"}`, "boot-token")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "a-decent-password", fa.lastUpdateCur)
	require.Equal(t, "alice", fa.lastUpdateUser)
}

func TestAuthSetupErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{auth.ErrCredentialExists, http.StatusConflict},
		{auth.ErrUsernameEmpty, http.StatusBadRequest},
		{auth.ErrUsernameTooLong, http.StatusBadRequest},
		{auth.ErrUsernameInvalid, http.StatusBadRequest},
		{auth.ErrPasswordTooShort, http.StatusBadRequest},
		{auth.ErrPasswordTooLong, http.StatusBadRequest},
	} {
		fa := newFakeAuth()
		fa.setupErr = tc.err
		s := New(Options{Version: "test-1", Started: time.Now(), Auth: fa})
		ts := httptest.NewServer(s.Handler())
		resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/setup", `{"username":"u","password":"p-1234567"}`, "")
		require.Equal(t, tc.status, resp.StatusCode, "for %v", tc.err)
		ts.Close()
	}
}

func TestAuthCredentialErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{auth.ErrBadCredentials, http.StatusUnauthorized},
		{auth.ErrRateLimited, http.StatusTooManyRequests},
		{auth.ErrPasswordTooShort, http.StatusBadRequest},
		{auth.ErrPasswordTooLong, http.StatusBadRequest},
		{auth.ErrUsernameEmpty, http.StatusBadRequest},
		{auth.ErrUsernameInvalid, http.StatusBadRequest},
	} {
		ts, fa, token := lockedServer(t, Options{})
		fa.updateErr = tc.err
		resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/credential", `{"current_password":"c","new_username":"bob","new_password":"n-123456"}`, token)
		require.Equal(t, tc.status, resp.StatusCode, "for %v", tc.err)
	}
}

func TestAuthCredentialIgnoresReadOnlyMode(t *testing.T) {
	// GANTRY_READ_ONLY is the docker write-path kill switch; being unable
	// to change the login on a read-only box would be a foot-gun. The two
	// switches are orthogonal by design.
	ts, fa, token := lockedServer(t, Options{ReadOnly: true})
	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/credential", `{"current_password":"c","new_username":"bob","new_password":"n-123456"}`, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "bob", fa.lastUpdateUser)
	require.Equal(t, "n-123456", fa.lastUpdatePass)
}

func TestHealthzSplitsBodyByAuthentication(t *testing.T) {
	ts, _, token := lockedServer(t, Options{
		Sources: func() map[string]string {
			return map[string]string{"docker": "unavailable: /var/run/docker.sock missing"}
		},
	})

	// Locked + anonymous: bare status only. The sources map carries
	// filesystem hint text and version/uptime fingerprint the box -- an
	// unauthenticated scanner gets none of it.
	resp := authReq(t, http.MethodGet, ts.URL+"/api/healthz", "", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var minimal map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&minimal))
	require.Equal(t, map[string]any{"status": "ok"}, minimal, "unauthenticated healthz must carry the status string only")

	// Locked + session: full detail.
	resp = authReq(t, http.MethodGet, ts.URL+"/api/healthz", "", token)
	var full map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&full))
	require.Contains(t, full, "version")
	require.Contains(t, full, "sources")

	// Gate off (nil Auth): full detail, unchanged open behavior.
	s := New(Options{Version: "test-1", Started: time.Now(), Sources: func() map[string]string { return map[string]string{"host": "ok"} }})
	ts2 := httptest.NewServer(s.Handler())
	defer ts2.Close()
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/healthz", "", "")
	full = nil
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&full))
	require.Contains(t, full, "version")
}
