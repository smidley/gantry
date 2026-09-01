package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// testManager wires a Manager over a real *store.Store with an
// injectable clock -- the same store the wired binary uses, so token
// hashing and expiry are exercised against real persistence, not a
// double.
func testManager(t *testing.T) (*Manager, *store.Store, *time.Time) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), func() time.Time { return now })
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	m, err := New(Options{
		Sessions:    st,
		Settings:    st,
		AppendEvent: st.AppendEvent,
		Mode:        ModeAuto,
		Now:         func() time.Time { return now },
	})
	require.NoError(t, err)
	return m, st, &now
}

func events(t *testing.T, st *store.Store, kind string) []store.Event {
	t.Helper()
	evs, err := st.QueryEvents(context.Background(), store.EventFilter{Kinds: []string{kind}})
	require.NoError(t, err)
	return evs
}

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", ModeAuto, true},
		{"auto", ModeAuto, true},
		{"proxy", ModeProxy, true},
		{"PROXY", ModeProxy, true},
		{"off", ModeAuto, false},
		{"yes", ModeAuto, false},
	} {
		got, err := ParseMode(tc.in)
		require.Equal(t, tc.want, got, "ParseMode(%q)", tc.in)
		if tc.ok {
			require.NoError(t, err)
		} else {
			require.Error(t, err, "unknown mode %q must error (caller logs and falls back to auto -- fail closed, never open)", tc.in)
		}
	}
}

func TestSetPasswordFirstSetNeedsNoCurrentAndIssuesSession(t *testing.T) {
	m, st, _ := testManager(t)
	require.False(t, m.PasswordSet())

	token, err := m.SetPassword("10.0.0.9", "", "a-decent-password")
	require.NoError(t, err)
	require.True(t, m.PasswordSet())
	require.True(t, m.Authenticate(token), "first-set must leave the caller logged in")

	// Stored as an argon2id PHC string under the settings key -- never
	// the password itself.
	v, ok, err := st.SettingGet("auth.password_hash")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(v, "$argon2id$"))
	require.NotContains(t, v, "a-decent-password")
}

func TestSetPasswordPolicyBounds(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "short7!")
	require.ErrorIs(t, err, ErrPasswordTooShort)
	_, err = m.SetPassword("ip", "", strings.Repeat("x", 257))
	require.ErrorIs(t, err, ErrPasswordTooLong)
	require.False(t, m.PasswordSet())
}

func TestSetPasswordChangeRequiresCurrentAndLogsOutOtherSessions(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "first-password")
	require.NoError(t, err)
	otherTab, err := m.Login("10.0.0.2", "first-password")
	require.NoError(t, err)

	_, err = m.SetPassword("10.0.0.1", "wrong-current", "second-password")
	require.ErrorIs(t, err, ErrBadCredentials)

	fresh, err := m.SetPassword("10.0.0.1", "first-password", "second-password")
	require.NoError(t, err)
	require.False(t, m.Authenticate(otherTab), "a password change must log out every other session")
	require.True(t, m.Authenticate(fresh), "the changer keeps a fresh session")

	_, err = m.Login("10.0.0.3", "first-password")
	require.ErrorIs(t, err, ErrBadCredentials)
	_, err = m.Login("10.0.0.3", "second-password")
	require.NoError(t, err)
}

func TestLoginNoPasswordConfigured(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.Login("10.0.0.1", "anything")
	require.ErrorIs(t, err, ErrNoPassword)
}

func TestLoginWrongPasswordFailsAndRateLimits(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)

	for i := 0; i < loginPerIPCapacity; i++ {
		_, err := m.Login("10.0.0.1", "not-it")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	_, err = m.Login("10.0.0.1", "not-it")
	require.ErrorIs(t, err, ErrRateLimited)
	// Even the RIGHT password is limited once the bucket is empty --
	// the limiter gates attempts, not outcomes, so it can't be used as
	// a correctness oracle.
	_, err = m.Login("10.0.0.1", "the-password")
	require.ErrorIs(t, err, ErrRateLimited)
	// Another IP is unaffected.
	_, err = m.Login("10.0.0.2", "the-password")
	require.NoError(t, err)
}

func TestLoginEventsOkAndThrottledFailures(t *testing.T) {
	m, st, now := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)

	_, err = m.Login("10.0.0.7", "the-password")
	require.NoError(t, err)
	okEvs := events(t, st, "auth.login_ok")
	require.Len(t, okEvs, 1)
	require.Equal(t, "10.0.0.7", okEvs[0].Entity)
	require.NotContains(t, okEvs[0].Detail, "the-password")

	// Six failed attempts inside the hour (five wrong passwords, then
	// one rate-limited -- both kinds count): only the FIRST logs
	// immediately (the alert dispatcher's once-per-entity-per-window
	// discipline); the rest are counted, and surface on the first
	// failure past the window with the suppressed count.
	for i := 0; i < loginPerIPCapacity; i++ {
		_, err := m.Login("10.0.0.8", "nope")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	_, err = m.Login("10.0.0.8", "nope") // limited now, still a failed attempt
	require.ErrorIs(t, err, ErrRateLimited)
	failEvs := events(t, st, "auth.login_failed")
	require.Len(t, failEvs, 1, "failures within the window must coalesce into the first event")
	require.Equal(t, "10.0.0.8", failEvs[0].Entity)

	*now = now.Add(61 * time.Minute)
	_, err = m.Login("10.0.0.8", "nope")
	require.ErrorIs(t, err, ErrBadCredentials)
	failEvs = events(t, st, "auth.login_failed")
	require.Len(t, failEvs, 2)
	require.Contains(t, failEvs[0].Detail, "5 more", "the post-window event must carry the suppressed count")
}

func TestAuthenticateRejectsUnknownAndGarbageTokens(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)
	require.False(t, m.Authenticate(""))
	require.False(t, m.Authenticate("not-a-real-token"))
}

func TestSessionTokenStoredAsDigestNotRaw(t *testing.T) {
	m, st, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)
	token, err := m.Login("10.0.0.1", "the-password")
	require.NoError(t, err)

	_, ok, err := st.GetSession(token)
	require.NoError(t, err)
	require.False(t, ok, "the raw token must never be a sessions key")
	sess, ok, err := st.GetSession(HashToken(token))
	require.NoError(t, err)
	require.True(t, ok, "the sessions key must be the token's SHA-256 hex digest")
	require.Len(t, sess.TokenHash, 64)
}

func TestSessionSlidingExpiry(t *testing.T) {
	m, _, now := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)
	token, err := m.Login("10.0.0.1", "the-password")
	require.NoError(t, err)

	// 6 days idle: inside the 7d sliding window, still valid -- and the
	// touch slides the window forward.
	*now = now.Add(6 * 24 * time.Hour)
	require.True(t, m.Authenticate(token))
	*now = now.Add(6 * 24 * time.Hour)
	require.True(t, m.Authenticate(token), "activity must have slid the window")

	// 8 days idle: past the sliding window.
	*now = now.Add(8 * 24 * time.Hour)
	require.False(t, m.Authenticate(token))
	require.False(t, m.Authenticate(token), "an expired session stays dead")
}

func TestSessionAbsoluteCapEndsEvenActiveSessions(t *testing.T) {
	m, _, now := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)
	token, err := m.Login("10.0.0.1", "the-password")
	require.NoError(t, err)

	// Touch every day: the sliding window never lapses, but the
	// absolute cap anchored at created_at still must.
	for day := 0; day < 29; day++ {
		*now = now.Add(24 * time.Hour)
		require.True(t, m.Authenticate(token), "day %d must still be valid", day+1)
	}
	*now = now.Add(2 * 24 * time.Hour)
	require.False(t, m.Authenticate(token), "no amount of activity extends a session past the absolute cap")
}

func TestLogoutDeletesTheSession(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)
	token, err := m.Login("10.0.0.1", "the-password")
	require.NoError(t, err)

	m.Logout(token)
	require.False(t, m.Authenticate(token))
	m.Logout(token) // idempotent
	m.Logout("garbage")
}

func TestDisableRequiresCurrentPasswordAndWipesSessions(t *testing.T) {
	m, st, _ := testManager(t)
	_, err := m.SetPassword("ip", "", "the-password")
	require.NoError(t, err)
	token, err := m.Login("10.0.0.1", "the-password")
	require.NoError(t, err)

	require.ErrorIs(t, m.Disable("10.0.0.1", "wrong"), ErrBadCredentials)
	require.True(t, m.PasswordSet())

	require.NoError(t, m.Disable("10.0.0.1", "the-password"))
	require.False(t, m.PasswordSet())
	require.False(t, m.Authenticate(token), "disable must wipe sessions, not leave them dangling")
	require.NoError(t, m.Disable("10.0.0.1", "whatever"), "disabling twice is a no-op")
	require.Len(t, events(t, st, "auth.disabled"), 1)
}

func TestEnsureEnvPasswordBootSemantics(t *testing.T) {
	m, st, _ := testManager(t)

	// First boot with the var: sets the password.
	require.NoError(t, m.EnsureEnvPassword("env-password-1"))
	require.True(t, m.PasswordSet())
	require.True(t, m.EnvManaged())
	token, err := m.Login("10.0.0.1", "env-password-1")
	require.NoError(t, err)

	// Same var on the next boot: hash verified equal, sessions survive.
	require.NoError(t, m.EnsureEnvPassword("env-password-1"))
	require.True(t, m.Authenticate(token), "an unchanged env password must not churn sessions on every boot")

	// Changed var: hash rewritten, sessions wiped (a password change).
	require.NoError(t, m.EnsureEnvPassword("env-password-2"))
	require.False(t, m.Authenticate(token))
	_, err = m.Login("10.0.0.1", "env-password-2")
	require.NoError(t, err)

	// Policy applies to the env path too, and a rejected env value
	// leaves the existing password (and EnvManaged reporting) alone.
	require.ErrorIs(t, m.EnsureEnvPassword("short"), ErrPasswordTooShort)
	_, err = m.Login("10.0.0.2", "env-password-2")
	require.NoError(t, err)

	// The stored hash never carries any env password in the clear.
	v, _, _ := st.SettingGet("auth.password_hash")
	require.NotContains(t, v, "env-password")
}

func TestEnsureEnvPasswordRecoversFromCorruptStoredHash(t *testing.T) {
	m, st, _ := testManager(t)
	require.NoError(t, st.SettingSet("auth.password_hash", "garbage-not-a-phc"))
	m2, err := New(Options{Sessions: st, Settings: st, Mode: ModeAuto, Now: m.now})
	require.NoError(t, err)
	require.True(t, m2.PasswordSet(), "a corrupt hash still counts as configured (fail closed)")

	// A corrupt hash can't verify anything -- but the env path rewrites
	// it rather than bricking auth until manual surgery.
	_, err = m2.Login("10.0.0.1", "anything-at-all")
	require.Error(t, err)
	require.NoError(t, m2.EnsureEnvPassword("fresh-env-password"))
	_, err = m2.Login("10.0.0.1", "fresh-env-password")
	require.NoError(t, err)
}

func TestManagerModeAndEnvManagedDefaults(t *testing.T) {
	m, _, _ := testManager(t)
	require.Equal(t, ModeAuto, m.Mode())
	require.False(t, m.EnvManaged())
}
