CREATE TABLE insight_instances (
    id             INTEGER PRIMARY KEY,
    rule_id        TEXT NOT NULL,             -- 'disk-io-contention' etc, from the compiled library
    -- The dedup identity: one active finding per (rule, victim, culprit, resource).
    victim_kind    TEXT NOT NULL,             -- container|host|array|disk|gpu
    victim         TEXT NOT NULL,             -- container name, slot, engine, '' for host
    culprit        TEXT NOT NULL,             -- container name; '' when the culprit is a set
    culprits       TEXT NOT NULL DEFAULT '',  -- comma-separated when >1 (the shared-culprit shape)
    resource       TEXT NOT NULL,             -- 'disk3' | 'cpu' | 'memory' | 'gpu:video'
    state          TEXT NOT NULL,             -- pending|active|resolved
    severity       TEXT NOT NULL,             -- info|warning|alert  (matches the alert vocabulary)
    confidence     TEXT NOT NULL,             -- likely|confirmed
    tier           TEXT NOT NULL,             -- proxy|psi  (which evidence actually fired it)
    statement      TEXT NOT NULL,             -- the rendered one-sentence finding, frozen at fire time
    evidence       TEXT NOT NULL,             -- JSON bundle: series ids, window, the actual numbers
    started_at     INTEGER NOT NULL,
    fired_at       INTEGER NOT NULL DEFAULT 0,
    resolved_at    INTEGER NOT NULL DEFAULT 0,
    resolve_reason TEXT NOT NULL DEFAULT '',  -- cleared|no-data|restart|rule-disabled|dismissed
    notified_at    INTEGER NOT NULL DEFAULT 0
);
-- One ACTIVE finding per identity tuple, enforced by the DB (the Phase 4
-- alert_instances precedent), not by engine bookkeeping.
CREATE UNIQUE INDEX idx_insight_active ON insight_instances
    (rule_id, victim, culprit, resource) WHERE resolved_at = 0;
CREATE INDEX idx_insight_started ON insight_instances (started_at);

-- Per-rule tuning + enablement. Thresholds only; rule SHAPE is compiled in.
CREATE TABLE insight_rule_config (
    rule_id     TEXT PRIMARY KEY,
    enabled     INTEGER NOT NULL DEFAULT 1,
    notify      INTEGER NOT NULL DEFAULT 0,   -- opt-in; see Task 8
    overrides   TEXT NOT NULL DEFAULT '',     -- JSON: threshold name -> value
    updated_at  INTEGER NOT NULL
);

-- "This wasn't useful" -- feedback without ML (Open question 3).
CREATE TABLE insight_dismissals (
    id         INTEGER PRIMARY KEY,
    rule_id    TEXT NOT NULL,
    victim     TEXT NOT NULL DEFAULT '',
    culprit    TEXT NOT NULL DEFAULT '',
    resource   TEXT NOT NULL DEFAULT '',
    until      INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
