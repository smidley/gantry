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
// convention. Sessions are a plain token set; errors are injected via
// the err fields using internal/auth's own sentinels, exactly what the
// real manager returns across this boundary.
type fakeAuth struct {
	mode        string
	passwordSet bool
	envManaged  bool

	valid map[string]bool // token -> live session

	loginToken string
	loginErr   error
	setToken   string
	setErr     error
	disableErr error

	lastLoginIP, lastLoginPassword        string
	lastSetIP, lastSetCurrent, lastSetNew string
	lastDisableIP, lastDisableCurrent     string
	loggedOut                             []string
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{mode: auth.ModeAuto, valid: map[string]bool{}}
}

func (f *fakeAuth) Mode() string      { return f.mode }
func (f *fakeAuth) PasswordSet() bool { return f.passwordSet }
func (f *fakeAuth) EnvManaged() bool  { return f.envManaged }

func (f *fakeAuth) Login(ip, password string) (string, error) {
	f.lastLoginIP, f.lastLoginPassword = ip, password
	if f.loginErr != nil {
		return "", f.loginErr
	}
	f.valid[f.loginToken] = true
	return f.loginToken, nil
}

func (f *fakeAuth) Authenticate(token string) bool { return f.valid[token] }

func (f *fakeAuth) Logout(token string) {
	f.loggedOut = append(f.loggedOut, token)
	delete(f.valid, token)
}

func (f *fakeAuth) SetPassword(ip, current, newPassword string) (string, error) {
	f.lastSetIP, f.lastSetCurrent, f.lastSetNew = ip, current, newPassword
	if f.setErr != nil {
		return "", f.setErr
	}
	f.valid = map[string]bool{f.setToken: true}
	f.passwordSet = true
	return f.setToken, nil
}

func (f *fakeAuth) Disable(ip, current string) error {
	f.lastDisableIP, f.lastDisableCurrent = ip, current
	if f.disableErr != nil {
		return f.disableErr
	}
	f.passwordSet = false
	f.valid = map[string]bool{}
	return nil
}

// lockedServer is a server with the gate ON: auto mode, password set,
// one valid session token.
func lockedServer(t *testing.T, opts Options) (*httptest.Server, *fakeAuth, string) {
	t.Helper()
	fa := newFakeAuth()
	fa.passwordSet = true
	const token = "valid-session-token"
	fa.valid[token] = true
	fa.loginToken = "fresh-login-token"
	fa.setToken = "fresh-set-token"
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

func TestAuthLoginSetsHardenedSessionCookie(t *testing.T) {
	ts, fa, _ := lockedServer(t, Options{})

	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"password":"pw-goes-here"}`, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "pw-goes-here", fa.lastLoginPassword)
	require.Equal(t, "127.0.0.1", fa.lastLoginIP, "the limiter must see the TCP peer's bare IP, not host:port")

	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c, "login must set the session cookie")
	require.Equal(t, "fresh-login-token", c.Value)
	require.True(t, c.HttpOnly, "script must never be able to read the token")
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, "/", c.Path, "every API route and the SSE stream need the cookie")
	require.False(t, c.Secure, "plain-HTTP LAN requests must not get a Secure cookie the browser would then drop")
	require.Equal(t, int(auth.SessionAbsoluteCap/time.Second), c.MaxAge)
}

func TestAuthLoginSecureCookieBehindTLSTerminatingProxy(t *testing.T) {
	ts, _, _ := lockedServer(t, Options{})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", strings.NewReader(`{"password":"x"}`))
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
		{auth.ErrNoPassword, http.StatusConflict},
	}
	for _, tc := range cases {
		ts, fa, _ := lockedServer(t, Options{})
		fa.loginErr = tc.err
		resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"password":"x"}`, "")
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

	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"password":"x","extra":true}`, "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown fields must fail the decode")

	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"password":"`+strings.Repeat("a", 8192)+`"}`, "")
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "a body over the auth cap must 413")
}

func TestAuthLoginUnavailableWithoutManagerOrInProxyMode(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Auth nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/login", `{"password":"x"}`, "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	ts2, fa, _ := lockedServer(t, Options{})
	fa.mode = auth.ModeProxy
	resp = authReq(t, http.MethodPost, ts2.URL+"/api/auth/login", `{"password":"x"}`, "")
	require.Equal(t, http.StatusConflict, resp.StatusCode, "proxy mode owns authentication; the built-in login must refuse")
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
	// Nil manager: an open zero-config install.
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	var st authStatusResponse
	resp := authReq(t, http.MethodGet, ts.URL+"/api/auth/status", "", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.Equal(t, authStatusResponse{Mode: auth.ModeAuto}, st)

	// Locked, no cookie vs valid cookie.
	ts2, fa, token := lockedServer(t, Options{})
	fa.envManaged = true
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/auth/status", "", "")
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.Equal(t, authStatusResponse{Mode: auth.ModeAuto, PasswordSet: true, EnvManaged: true}, st)

	resp = authReq(t, http.MethodGet, ts2.URL+"/api/auth/status", "", token)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	require.True(t, st.Authenticated)

	// The response must never leak hash material of any kind -- checked
	// on the raw bytes, not a decoded struct that could silently drop
	// an unexpected field.
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/auth/status", "", "")
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "argon2")
	require.NotContains(t, string(raw), "hash")
}

func TestAuthPasswordFirstSetOpenThenChangeGated(t *testing.T) {
	// Gate OFF (no password yet): first-set must be reachable with no
	// session -- the bootstrap. The fake flips passwordSet true.
	fa := newFakeAuth()
	fa.setToken = "boot-token"
	s := New(Options{Version: "test-1", Started: time.Now(), Auth: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/password", `{"current_password":"","new_password":"a-decent-password"}`, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "a-decent-password", fa.lastSetNew)
	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c, "first-set must leave the caller logged in")
	require.Equal(t, "boot-token", c.Value)

	// Gate now ON: the same route without a session 401s (mux-wide
	// gate), and with one it reaches the handler again.
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/password", `{"current_password":"a","new_password":"another-password"}`, "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/password", `{"current_password":"a-decent-password","new_password":"another-password"}`, "boot-token")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "a-decent-password", fa.lastSetCurrent)
}

func TestAuthPasswordErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{auth.ErrBadCredentials, http.StatusUnauthorized},
		{auth.ErrRateLimited, http.StatusTooManyRequests},
		{auth.ErrPasswordTooShort, http.StatusBadRequest},
		{auth.ErrPasswordTooLong, http.StatusBadRequest},
	} {
		ts, fa, token := lockedServer(t, Options{})
		fa.setErr = tc.err
		resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/password", `{"current_password":"c","new_password":"n-123456"}`, token)
		require.Equal(t, tc.status, resp.StatusCode, "for %v", tc.err)
	}
}

func TestAuthPasswordIgnoresReadOnlyMode(t *testing.T) {
	// GANTRY_READ_ONLY is the docker write-path kill switch; being
	// unable to SECURE a read-only box would invert its purpose. The
	// two switches are orthogonal by design.
	ts, fa, token := lockedServer(t, Options{ReadOnly: true})
	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/password", `{"current_password":"c","new_password":"n-123456"}`, token)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "n-123456", fa.lastSetNew)
}

func TestAuthDisable(t *testing.T) {
	ts, fa, token := lockedServer(t, Options{})
	fa.disableErr = auth.ErrBadCredentials
	resp := authReq(t, http.MethodPost, ts.URL+"/api/auth/disable", `{"current_password":"wrong"}`, token)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	fa.disableErr = nil
	resp = authReq(t, http.MethodPost, ts.URL+"/api/auth/disable", `{"current_password":"right"}`, token)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, "right", fa.lastDisableCurrent)
	c := sessionCookieFrom(t, resp)
	require.NotNil(t, c)
	require.Less(t, c.MaxAge, 0, "disable must expire the cookie")
	require.False(t, fa.passwordSet)
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

	// Open install (nil Auth): full detail, unchanged zero-config
	// behavior.
	s := New(Options{Version: "test-1", Started: time.Now(), Sources: func() map[string]string { return map[string]string{"host": "ok"} }})
	ts2 := httptest.NewServer(s.Handler())
	defer ts2.Close()
	resp = authReq(t, http.MethodGet, ts2.URL+"/api/healthz", "", "")
	full = nil
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&full))
	require.Contains(t, full, "version")
}
