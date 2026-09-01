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

// testStore opens a real *store.Store with an injectable clock -- the
// same store the wired binary uses, so token hashing and expiry are
// exercised against real persistence, not a double.
func testStore(t *testing.T, now *time.Time) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), func() time.Time { return *now })
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// testManager wires a Manager over a real store with the injectable
// clock. The returned *time.Time is the shared clock both the store and
// the manager read, so advancing it drives expiry and limiter refill.
func testManager(t *testing.T) (*Manager, *store.Store, *time.Time) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	st := testStore(t, &now)
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

// setup is the common first-run bootstrap most tests need before they can
// log in.
func (m *Manager) mustSetup(t *testing.T, username, password string) string {
	t.Helper()
	token, err := m.Setup("10.0.0.9", username, password)
	require.NoError(t, err)
	return token
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
		{"none", ModeNone, true},
		{"NONE", ModeNone, true},
		{"off", ModeAuto, false},
		{"yes", ModeAuto, false},
		{"disabled", ModeAuto, false},
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

func TestSetupCreatesCredentialAndIssuesSession(t *testing.T) {
	m, st, _ := testManager(t)
	require.False(t, m.CredentialSet())
	require.Empty(t, m.Username())

	token, err := m.Setup("10.0.0.9", "  alice  ", "a-decent-password")
	require.NoError(t, err)
	require.True(t, m.CredentialSet())
	require.Equal(t, "alice", m.Username(), "the username is trimmed before it is stored")
	require.True(t, m.Authenticate(token), "setup must leave the caller logged in")

	// The password is stored only as an argon2id PHC string; the username
	// is stored in the clear (it is not a secret).
	hash, ok, err := st.SettingGet("auth.password_hash")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(hash, "$argon2id$"))
	require.NotContains(t, hash, "a-decent-password")
	user, ok, err := st.SettingGet("auth.username")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "alice", user)

	require.Len(t, events(t, st, "auth.setup"), 1)
}

func TestSetupIsOneShot(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "a-decent-password")

	// Once a credential exists, setup refuses forever -- regardless of the
	// body it carries.
	_, err := m.Setup("10.0.0.1", "mallory", "another-password")
	require.ErrorIs(t, err, ErrCredentialExists)
	require.Equal(t, "alice", m.Username(), "a rejected setup must not touch the existing credential")
}

func TestSetupPolicyBounds(t *testing.T) {
	m, _, _ := testManager(t)
	// Username rules.
	_, err := m.Setup("ip", "   ", "a-decent-password")
	require.ErrorIs(t, err, ErrUsernameEmpty)
	_, err = m.Setup("ip", strings.Repeat("u", usernameMaxLen+1), "a-decent-password")
	require.ErrorIs(t, err, ErrUsernameTooLong)
	_, err = m.Setup("ip", "a\x00d", "a-decent-password")
	require.ErrorIs(t, err, ErrUsernameInvalid)
	_, err = m.Setup("ip", "line\nbreak", "a-decent-password")
	require.ErrorIs(t, err, ErrUsernameInvalid)
	// Password rules.
	_, err = m.Setup("ip", "alice", "short7!")
	require.ErrorIs(t, err, ErrPasswordTooShort)
	_, err = m.Setup("ip", "alice", strings.Repeat("x", passwordMaxLen+1))
	require.ErrorIs(t, err, ErrPasswordTooLong)

	require.False(t, m.CredentialSet(), "no rejected setup may leave a partial credential")
}

func TestLoginRequiresBothUsernameAndPassword(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")

	// Right username, wrong password.
	_, err := m.Login("10.0.0.1", "alice", "not-it")
	require.ErrorIs(t, err, ErrBadCredentials)
	// Wrong username, RIGHT password -- must still fail, and with the same
	// generic error (no hint which half was wrong).
	_, err = m.Login("10.0.0.2", "bob", "the-password")
	require.ErrorIs(t, err, ErrBadCredentials)
	// Both right.
	token, err := m.Login("10.0.0.3", "alice", "the-password")
	require.NoError(t, err)
	require.True(t, m.Authenticate(token))
	// The username is trimmed on the way in, matching how it was stored.
	_, err = m.Login("10.0.0.4", "  alice  ", "the-password")
	require.NoError(t, err)
}

func TestLoginWrongUsernameIsAFullCountedAttempt(t *testing.T) {
	// A wrong username takes exactly the same path as a wrong password:
	// it spends a limiter token and records an auth.login_failed event.
	// That path always runs the argon2 derivation (Login's doc), so a
	// wrong username cannot be told from a wrong password by timing.
	m, st, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")

	for i := 0; i < loginPerIPCapacity; i++ {
		_, err := m.Login("10.0.0.8", "wronguser", "the-password")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	// Bucket now dry: even the RIGHT credential is rate-limited, so the
	// wrong-username attempts really did consume the same tokens.
	_, err := m.Login("10.0.0.8", "alice", "the-password")
	require.ErrorIs(t, err, ErrRateLimited)
	// And the wrong-username run was audited as a failed login, not
	// silently dropped.
	require.Len(t, events(t, st, "auth.login_failed"), 1)
}

func TestLoginNoCredentialConfigured(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.Login("10.0.0.1", "anyone", "anything")
	require.ErrorIs(t, err, ErrNoCredential)
}

func TestLoginRateLimitGatesAttemptsNotOutcomes(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")

	for i := 0; i < loginPerIPCapacity; i++ {
		_, err := m.Login("10.0.0.1", "alice", "not-it")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	_, err := m.Login("10.0.0.1", "alice", "not-it")
	require.ErrorIs(t, err, ErrRateLimited)
	// Even the RIGHT password is limited once the bucket is empty -- the
	// limiter gates attempts, not outcomes, so it can't be a correctness
	// oracle.
	_, err = m.Login("10.0.0.1", "alice", "the-password")
	require.ErrorIs(t, err, ErrRateLimited)
	// Another IP is unaffected.
	_, err = m.Login("10.0.0.2", "alice", "the-password")
	require.NoError(t, err)
}

func TestLoginEventsOkAndThrottledFailures(t *testing.T) {
	m, st, now := testManager(t)
	m.mustSetup(t, "alice", "the-password")

	_, err := m.Login("10.0.0.7", "alice", "the-password")
	require.NoError(t, err)
	okEvs := events(t, st, "auth.login_ok")
	require.Len(t, okEvs, 1)
	require.Equal(t, "10.0.0.7", okEvs[0].Entity)
	require.NotContains(t, okEvs[0].Detail, "the-password")

	// Six failed attempts inside the hour (five wrong, then one
	// rate-limited): only the FIRST logs immediately; the rest are
	// counted and surface on the first failure past the window.
	for i := 0; i < loginPerIPCapacity; i++ {
		_, err := m.Login("10.0.0.8", "alice", "nope")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	_, err = m.Login("10.0.0.8", "alice", "nope") // limited now, still a failed attempt
	require.ErrorIs(t, err, ErrRateLimited)
	failEvs := events(t, st, "auth.login_failed")
	require.Len(t, failEvs, 1, "failures within the window must coalesce into the first event")
	require.Equal(t, "10.0.0.8", failEvs[0].Entity)

	*now = now.Add(61 * time.Minute)
	_, err = m.Login("10.0.0.8", "alice", "nope")
	require.ErrorIs(t, err, ErrBadCredentials)
	failEvs = events(t, st, "auth.login_failed")
	require.Len(t, failEvs, 2)
	require.Contains(t, failEvs[0].Detail, "5 more", "the post-window event must carry the suppressed count")
}

func TestAuthenticateRejectsUnknownAndGarbageTokens(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")
	require.False(t, m.Authenticate(""))
	require.False(t, m.Authenticate("not-a-real-token"))
}

func TestSessionTokenStoredAsDigestNotRaw(t *testing.T) {
	m, st, _ := testManager(t)
	token := m.mustSetup(t, "alice", "the-password")

	_, ok, err := st.GetSession(token)
	require.NoError(t, err)
	require.False(t, ok, "the raw token must never be a sessions key")
	sess, ok, err := st.GetSession(HashToken(token))
	require.NoError(t, err)
	require.True(t, ok, "the sessions key must be the token's SHA-256 hex digest")
	require.Len(t, sess.TokenHash, 64)
}

func TestSessionIdleTimeoutEndsSilentSessions(t *testing.T) {
	m, _, now := testManager(t)
	m.mustSetup(t, "alice", "the-password")
	token, err := m.Login("10.0.0.1", "alice", "the-password")
	require.NoError(t, err)

	// 7h idle: inside the 8h idle window, still valid -- and the touch
	// slides the window forward.
	*now = now.Add(7 * time.Hour)
	require.True(t, m.Authenticate(token))
	*now = now.Add(7 * time.Hour)
	require.True(t, m.Authenticate(token), "activity must have slid the idle window")

	// 9h of silence: past the idle window (and still shy of the 24h
	// absolute cap, so this isolates the idle backstop).
	*now = now.Add(9 * time.Hour)
	require.False(t, m.Authenticate(token))
	require.False(t, m.Authenticate(token), "an expired session stays dead")
}

func TestSessionAbsoluteCapEndsEvenActiveSessions(t *testing.T) {
	m, _, now := testManager(t)
	m.mustSetup(t, "alice", "the-password")
	token, err := m.Login("10.0.0.1", "alice", "the-password")
	require.NoError(t, err)

	// Touch every hour: the idle window never lapses, but the absolute
	// cap anchored at created_at (24h) still must.
	for h := 0; h < 23; h++ {
		*now = now.Add(time.Hour)
		require.True(t, m.Authenticate(token), "hour %d must still be valid", h+1)
	}
	*now = now.Add(2 * time.Hour)
	require.False(t, m.Authenticate(token), "no amount of activity extends a session past the absolute cap")
}

func TestLogoutDeletesTheSession(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")
	token, err := m.Login("10.0.0.1", "alice", "the-password")
	require.NoError(t, err)

	m.Logout(token)
	require.False(t, m.Authenticate(token))
	m.Logout(token) // idempotent
	m.Logout("garbage")
}

func TestUpdateCredentialRequiresCurrentAndLogsOutOtherSessions(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "first-password")
	otherTab, err := m.Login("10.0.0.2", "alice", "first-password")
	require.NoError(t, err)

	// Wrong current password: rejected, nothing changes.
	_, err = m.UpdateCredential("10.0.0.1", "wrong-current", "alice", "second-password")
	require.ErrorIs(t, err, ErrBadCredentials)

	// Right current password + a new password: every other session dies,
	// the caller keeps a fresh one, and the new password is now live.
	fresh, err := m.UpdateCredential("10.0.0.1", "first-password", "alice", "second-password")
	require.NoError(t, err)
	require.False(t, m.Authenticate(otherTab), "a credential change must log out every other session")
	require.True(t, m.Authenticate(fresh), "the changer keeps a fresh session")

	_, err = m.Login("10.0.0.3", "alice", "first-password")
	require.ErrorIs(t, err, ErrBadCredentials)
	_, err = m.Login("10.0.0.3", "alice", "second-password")
	require.NoError(t, err)
}

func TestUpdateCredentialChangesUsername(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")

	// New username, blank new password: a username-only change keeps the
	// existing password working.
	_, err := m.UpdateCredential("10.0.0.1", "the-password", "  bob  ", "")
	require.NoError(t, err)
	require.Equal(t, "bob", m.Username())
	_, err = m.Login("10.0.0.2", "bob", "the-password")
	require.NoError(t, err, "the old password must still verify after a username-only change")
	_, err = m.Login("10.0.0.2", "alice", "the-password")
	require.ErrorIs(t, err, ErrBadCredentials, "the old username must no longer log in")
}

func TestUpdateCredentialPolicyBounds(t *testing.T) {
	m, _, _ := testManager(t)
	m.mustSetup(t, "alice", "the-password")

	_, err := m.UpdateCredential("ip", "the-password", "", "")
	require.ErrorIs(t, err, ErrUsernameEmpty)
	_, err = m.UpdateCredential("ip", "the-password", "bob", "short7!")
	require.ErrorIs(t, err, ErrPasswordTooShort)
	// The rejected changes left the original credential intact.
	_, err = m.Login("10.0.0.2", "alice", "the-password")
	require.NoError(t, err)
}

func TestUpdateCredentialRequiresExistingCredential(t *testing.T) {
	m, _, _ := testManager(t)
	_, err := m.UpdateCredential("ip", "whatever", "alice", "a-decent-password")
	require.ErrorIs(t, err, ErrNoCredential)
}

func TestEnsureEnvCredentialBootSemantics(t *testing.T) {
	m, st, _ := testManager(t)

	// First boot with both vars: sets the credential.
	require.NoError(t, m.EnsureEnvCredential("admin", "env-password-1"))
	require.True(t, m.CredentialSet())
	require.True(t, m.EnvManaged())
	require.Equal(t, "admin", m.Username())
	token, err := m.Login("10.0.0.1", "admin", "env-password-1")
	require.NoError(t, err)

	// Same pair on the next boot: verified equal, sessions survive.
	require.NoError(t, m.EnsureEnvCredential("admin", "env-password-1"))
	require.True(t, m.Authenticate(token), "an unchanged env credential must not churn sessions on every boot")

	// Changed password: rewritten, sessions wiped.
	require.NoError(t, m.EnsureEnvCredential("admin", "env-password-2"))
	require.False(t, m.Authenticate(token))
	_, err = m.Login("10.0.0.1", "admin", "env-password-2")
	require.NoError(t, err)

	// Changed username (same password) is also a change: sessions wiped,
	// the old username stops working.
	token2, err := m.Login("10.0.0.1", "admin", "env-password-2")
	require.NoError(t, err)
	require.NoError(t, m.EnsureEnvCredential("root", "env-password-2"))
	require.False(t, m.Authenticate(token2), "a username change through the env path wipes sessions too")
	require.Equal(t, "root", m.Username())
	_, err = m.Login("10.0.0.1", "admin", "env-password-2")
	require.ErrorIs(t, err, ErrBadCredentials)
	_, err = m.Login("10.0.0.1", "root", "env-password-2")
	require.NoError(t, err)

	// Policy applies to the env path too, and a rejected value leaves the
	// existing credential (and EnvManaged reporting) alone.
	require.ErrorIs(t, m.EnsureEnvCredential("root", "short"), ErrPasswordTooShort)
	require.ErrorIs(t, m.EnsureEnvCredential("", "env-password-2"), ErrUsernameEmpty)
	_, err = m.Login("10.0.0.2", "root", "env-password-2")
	require.NoError(t, err)

	// The stored hash never carries any env password in the clear.
	v, _, _ := st.SettingGet("auth.password_hash")
	require.NotContains(t, v, "env-password")
}

func TestEnsureEnvCredentialRecoversFromCorruptStoredHash(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	st := testStore(t, &now)
	// A corrupt hash WITH a username already present -- no migration, so
	// this isolates the corrupt-hash recovery path.
	require.NoError(t, st.SettingSet("auth.password_hash", "garbage-not-a-phc"))
	require.NoError(t, st.SettingSet("auth.username", "root"))
	m, err := New(Options{Sessions: st, Settings: st, Mode: ModeAuto, Now: func() time.Time { return now }})
	require.NoError(t, err)
	require.True(t, m.CredentialSet(), "a corrupt hash still counts as configured (fail closed)")

	// The corrupt hash can't verify anything -- but the env path rewrites
	// it rather than bricking auth until manual surgery.
	_, err = m.Login("10.0.0.1", "root", "anything-at-all")
	require.Error(t, err)
	require.NoError(t, m.EnsureEnvCredential("root", "fresh-env-password"))
	_, err = m.Login("10.0.0.1", "root", "fresh-env-password")
	require.NoError(t, err)
}

func TestMigratesPasswordOnlyCredentialToAdmin(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	st := testStore(t, &now)
	// Simulate a pre-0.1.1 install: a real password hash, no username row.
	phc, err := HashPassword("legacy-password")
	require.NoError(t, err)
	require.NoError(t, st.SettingSet("auth.password_hash", phc))

	m, err := New(Options{Sessions: st, Settings: st, Mode: ModeAuto, Now: func() time.Time { return now }})
	require.NoError(t, err)
	require.True(t, m.CredentialSet())
	require.Equal(t, "admin", m.Username(), "a password-only install must migrate to username admin, not lock the owner out")

	// The migrated username was persisted, and the existing password still
	// verifies under it.
	stored, ok, err := st.SettingGet("auth.username")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "admin", stored)
	_, err = m.Login("10.0.0.1", "admin", "legacy-password")
	require.NoError(t, err)
}

func TestManagerModeAndEnvManagedDefaults(t *testing.T) {
	m, _, _ := testManager(t)
	require.Equal(t, ModeAuto, m.Mode())
	require.False(t, m.EnvManaged())
	require.False(t, m.CredentialSet())
}
