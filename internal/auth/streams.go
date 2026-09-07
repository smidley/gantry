package auth

import (
	"context"
	"time"
)

// WatchSession cancels access when a session is revoked or expires. Merely
// keeping a stream open does not extend the session's idle lifetime.
func (m *Manager) WatchSession(parent context.Context, token string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		defer cancel()
		for {
			// Subscribe before reading, so a concurrent revocation cannot be lost.
			m.mu.Lock()
			changed := m.sessionChanged
			m.mu.Unlock()
			sess, ok, err := m.sessions.GetSession(HashToken(token))
			if err != nil || !ok {
				return
			}
			deadline := min(sess.ExpiresAt, sess.CreatedAt+int64(SessionAbsoluteCap/time.Second))
			remaining := time.Unix(deadline, 0).Sub(m.now())
			if remaining <= 0 {
				return
			}
			// The periodic backstop also catches expiry under an injected clock
			// or a session removed by another process sharing the database.
			timer := time.NewTimer(min(remaining, time.Second))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-changed:
			case <-timer.C:
			}
			timer.Stop()
		}
	}()
	return ctx, cancel
}

func (m *Manager) notifySessionChange() {
	m.mu.Lock()
	close(m.sessionChanged)
	m.sessionChanged = make(chan struct{})
	m.mu.Unlock()
}
