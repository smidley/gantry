package store

import (
	"context"
	"database/sql"
	"time"
)

// Session is one sessions row -- see migrations/006_sessions.sql for
// what each field means and why TokenHash is a digest, never the raw
// cookie token. All policy (expiry math, token hashing, sliding-window
// touches) lives in internal/auth; this file is plain persistence.
type Session struct {
	TokenHash string
	CreatedAt int64
	LastSeen  int64
	ExpiresAt int64
}

func (s *Store) InsertSession(sess Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, created_at, last_seen, expires_at) VALUES (?,?,?,?)`,
		sess.TokenHash, sess.CreatedAt, sess.LastSeen, sess.ExpiresAt)
	return err
}

// GetSession reads one session off the read pool (s.readDB, same as
// every other per-request read path -- never s.db, which Maintain's
// multi-second flush lock can hold). ok is false for an unknown hash.
func (s *Store) GetSession(tokenHash string) (Session, bool, error) {
	var sess Session
	err := s.readDB.QueryRow(`SELECT token_hash, created_at, last_seen, expires_at FROM sessions WHERE token_hash=?`,
		tokenHash).Scan(&sess.TokenHash, &sess.CreatedAt, &sess.LastSeen, &sess.ExpiresAt)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return sess, true, nil
}

// TouchSession advances one session's last_seen and expires_at.
// created_at is deliberately not writable through any Store method: the
// absolute-cap clamp anchors to it, so a bug (or a hostile client) must
// have no path to push it forward. A hash that no longer exists is a
// silent no-op -- the session raced with a logout or a prune, and either
// way it's gone.
func (s *Store) TouchSession(tokenHash string, lastSeen, expiresAt int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_seen=?, expires_at=? WHERE token_hash=?`,
		lastSeen, expiresAt, tokenHash)
	return err
}

// DeleteSession removes one session (logout, or lazy expiry cleanup).
// Idempotent: deleting an already-gone hash is a no-op.
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// DeleteAllSessions removes every session -- the "log out other
// sessions" half of a password change (internal/auth wipes all, then
// issues the caller a fresh one). Returns how many rows went.
func (s *Store) DeleteAllSessions() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneSessions deletes every session whose expires_at has passed --
// Maintain's sweep for browsers that never came back to trigger the
// lazy per-request deletion.
func (s *Store) PruneSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	return err
}
