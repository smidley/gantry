package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionsRoundTrip(t *testing.T) {
	s := newTestStore(t, nil)

	_, ok, err := s.GetSession("deadbeef")
	require.NoError(t, err)
	require.False(t, ok, "unknown token hash must read back as absent, not an error")

	sess := Session{TokenHash: "deadbeef", CreatedAt: 1000, LastSeen: 1000, ExpiresAt: 1000 + 3600}
	require.NoError(t, s.InsertSession(sess))

	got, ok, err := s.GetSession("deadbeef")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sess, got)
}

func TestSessionsTouchUpdatesLastSeenAndExpiry(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.InsertSession(Session{TokenHash: "aa", CreatedAt: 1000, LastSeen: 1000, ExpiresAt: 2000}))

	require.NoError(t, s.TouchSession("aa", 1500, 2500))

	got, ok, err := s.GetSession("aa")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(1000), got.CreatedAt, "created_at is immutable -- the absolute cap anchors to it")
	require.Equal(t, int64(1500), got.LastSeen)
	require.Equal(t, int64(2500), got.ExpiresAt)

	// Touching an id that no longer exists (raced with a delete) is a
	// silent no-op, not an error -- the session is simply gone.
	require.NoError(t, s.TouchSession("gone", 1500, 2500))
}

func TestSessionsDelete(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.InsertSession(Session{TokenHash: "aa", CreatedAt: 1, LastSeen: 1, ExpiresAt: 10}))

	require.NoError(t, s.DeleteSession("aa"))
	_, ok, err := s.GetSession("aa")
	require.NoError(t, err)
	require.False(t, ok)

	// Deleting twice (logout raced with expiry) is a silent no-op.
	require.NoError(t, s.DeleteSession("aa"))
}

func TestSessionsDeleteAll(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.InsertSession(Session{TokenHash: "aa", CreatedAt: 1, LastSeen: 1, ExpiresAt: 10}))
	require.NoError(t, s.InsertSession(Session{TokenHash: "bb", CreatedAt: 2, LastSeen: 2, ExpiresAt: 20}))

	n, err := s.DeleteAllSessions()
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	_, ok, _ := s.GetSession("aa")
	require.False(t, ok)
	_, ok, _ = s.GetSession("bb")
	require.False(t, ok)
}

func TestSessionsPruneRemovesOnlyExpired(t *testing.T) {
	s := newTestStore(t, nil)
	require.NoError(t, s.InsertSession(Session{TokenHash: "old", CreatedAt: 1, LastSeen: 1, ExpiresAt: 999}))
	require.NoError(t, s.InsertSession(Session{TokenHash: "live", CreatedAt: 1, LastSeen: 1, ExpiresAt: 2000}))

	require.NoError(t, s.PruneSessions(context.Background(), time.Unix(1000, 0)))

	_, ok, _ := s.GetSession("old")
	require.False(t, ok, "a session whose expires_at has passed must be pruned")
	_, ok, _ = s.GetSession("live")
	require.True(t, ok, "an unexpired session must survive the prune")
}

// TestMaintainPrunesExpiredSessions pins that the periodic maintenance
// tick covers sessions too -- lazy per-request expiry deletion (the auth
// manager's own job) can never clean up a session whose browser simply
// never came back.
func TestMaintainPrunesExpiredSessions(t *testing.T) {
	now := time.Unix(50_000, 0)
	s := newTestStore(t, func() time.Time { return now })
	require.NoError(t, s.InsertSession(Session{TokenHash: "old", CreatedAt: 1, LastSeen: 1, ExpiresAt: now.Unix() - 1}))

	require.NoError(t, s.Maintain(context.Background(), now, DefaultRetention()))

	_, ok, _ := s.GetSession("old")
	require.False(t, ok)
}
