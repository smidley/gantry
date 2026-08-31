-- Auth sessions: one row per logged-in browser (internal/auth). The
-- primary key is the SHA-256 hex digest of the session cookie's random
-- token -- the raw token itself is NEVER stored, so a copy of this
-- database (a backup, an appdata share exposed over SMB) cannot be
-- replayed into a live session.
--
-- Expiry is two-tiered, both enforced by the auth manager, not SQL:
-- expires_at slides forward on activity (last_seen tracks that) but is
-- always clamped to created_at + an absolute cap, so no session lives
-- forever just by staying busy. Expired rows are deleted lazily on the
-- request that finds them and swept by Maintain's periodic prune for
-- the browsers that never come back.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,  -- hex SHA-256 of the raw cookie token
    created_at INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);
