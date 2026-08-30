CREATE TABLE alert_rules (
    id                TEXT PRIMARY KEY,          -- stable slug, e.g. "disk-temp-high"
    name              TEXT NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    builtin           INTEGER NOT NULL DEFAULT 0, -- seeded default: disable-only, never deletable
    type              TEXT NOT NULL,             -- 'threshold' | 'event'
    kind              TEXT NOT NULL DEFAULT '',  -- host|container|disk|gpu|unraid
    entity_glob       TEXT NOT NULL DEFAULT '*',
    entity_class      TEXT NOT NULL DEFAULT '',  -- '' any; 'nvme'; '!nvme' negates
    metric            TEXT NOT NULL DEFAULT '',  -- threshold rules
    op                TEXT NOT NULL DEFAULT '>', -- '>' | '<'
    threshold         REAL NOT NULL DEFAULT 0,   -- FIRE boundary (== the band's "serious")
    clear_threshold   REAL NOT NULL DEFAULT 0,   -- hysteresis EXIT boundary (engine-only)
    warn_threshold    REAL NOT NULL DEFAULT 0,   -- display band only, never fires
    critical_threshold REAL NOT NULL DEFAULT 0,  -- display band only; 0 = family has no 4th tier
    band_family       TEXT NOT NULL DEFAULT '',  -- thresholds.ts MetricFamily this rule drives; '' = none
    for_seconds       INTEGER NOT NULL DEFAULT 0,
    clear_seconds     INTEGER NOT NULL DEFAULT 0,
    event_kinds       TEXT NOT NULL DEFAULT '',  -- comma-separated; event rules
    min_severity      TEXT NOT NULL DEFAULT '',  -- info|warning|alert floor on the source event
    clear_event_kinds TEXT NOT NULL DEFAULT '',  -- '' = timeout-only auto-resolve
    clear_max_severity TEXT NOT NULL DEFAULT '',
    severity          TEXT NOT NULL DEFAULT 'warning',
    channels          TEXT NOT NULL DEFAULT '',  -- '' = every enabled channel; else comma-separated ids
    renotify_hours    INTEGER NOT NULL DEFAULT 0,
    updated_at        INTEGER NOT NULL
);

CREATE TABLE alert_instances (
    id               INTEGER PRIMARY KEY,
    rule_id          TEXT NOT NULL,
    kind             TEXT NOT NULL,
    entity           TEXT NOT NULL,
    metric           TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL,             -- pending|firing|resolved
    severity         TEXT NOT NULL,
    value            REAL NOT NULL DEFAULT 0,
    threshold        REAL NOT NULL DEFAULT 0,   -- snapshot at fire time (a later rule edit must not rewrite history)
    summary          TEXT NOT NULL DEFAULT '',
    started_at       INTEGER NOT NULL,          -- first breach
    fired_at         INTEGER NOT NULL DEFAULT 0,
    resolved_at      INTEGER NOT NULL DEFAULT 0,
    resolve_reason   TEXT NOT NULL DEFAULT '',  -- cleared|timeout|no-data|rule-disabled
    last_notified_at INTEGER NOT NULL DEFAULT 0,
    notify_count     INTEGER NOT NULL DEFAULT 0
);
-- One ACTIVE instance per (rule, entity), enforced by the DB, not by engine bookkeeping.
CREATE UNIQUE INDEX idx_alert_active ON alert_instances (rule_id, entity) WHERE resolved_at = 0;
CREATE INDEX idx_alert_instances_started ON alert_instances (started_at);

CREATE TABLE alert_silences (
    id         INTEGER PRIMARY KEY,
    rule_id    TEXT NOT NULL DEFAULT '',   -- '' = any rule
    entity     TEXT NOT NULL DEFAULT '',   -- '' = any entity
    until      INTEGER NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE alert_deliveries (
    id          INTEGER PRIMARY KEY,
    instance_id INTEGER NOT NULL,
    ts          INTEGER NOT NULL,
    channel     TEXT NOT NULL,             -- 'notify' | 'webhook'
    target      TEXT NOT NULL DEFAULT '',  -- webhook target id; '' for notify
    phase       TEXT NOT NULL,             -- 'fired' | 'resolved' | 'renotify'
    attempts    INTEGER NOT NULL DEFAULT 1,
    ok          INTEGER NOT NULL,
    status      INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_alert_deliveries_ts ON alert_deliveries (ts);
