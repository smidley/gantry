package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamSessionsEndOnRevocation(t *testing.T) {
	for _, action := range []string{"logout", "credential-change", "environment-change", "expiry"} {
		t.Run(action, func(t *testing.T) {
			m, st, now := testManager(t)
			token := m.mustSetup(t, "operator", "initial-password")
			ctx, cancel := m.WatchSession(context.Background(), token)
			defer cancel()
			switch action {
			case "logout":
				m.Logout(token)
			case "credential-change":
				_, err := m.UpdateCredential("local", "initial-password", "operator", "changed-password")
				require.NoError(t, err)
			case "environment-change":
				require.NoError(t, m.EnsureEnvCredential("operator", "environment-password"))
			case "expiry":
				// Change persisted expiry, not the shared clock read by the watcher.
				require.NoError(t, st.TouchSession(HashToken(token), now.Unix(), now.Unix()-1))
				m.notifySessionChange()
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("revoked session retained stream access")
			}
		})
	}
}

func TestLogoutLeavesOtherSessionStreamOpen(t *testing.T) {
	m, _, _ := testManager(t)
	first := m.mustSetup(t, "operator", "initial-password")
	second, err := m.Login("local", "operator", "initial-password")
	require.NoError(t, err)
	ctx, cancel := m.WatchSession(context.Background(), second)
	defer cancel()
	m.Logout(first)
	require.True(t, m.Authenticate(second))
	select {
	case <-ctx.Done():
		t.Fatal("another session's logout revoked this stream")
	case <-time.After(20 * time.Millisecond):
	}
}
