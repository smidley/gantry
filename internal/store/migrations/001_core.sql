CREATE TABLE series (
    id         INTEGER PRIMARY KEY,
    kind       TEXT NOT NULL,
    entity     TEXT NOT NULL,
    metric     TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (kind, entity, metric)
);

CREATE TABLE samples_1m (
    series_id INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    avg       REAL NOT NULL,
    max       REAL NOT NULL,
    PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE samples_10m (
    series_id INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    avg       REAL NOT NULL,
    max       REAL NOT NULL,
    PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE samples_1h (
    series_id INTEGER NOT NULL,
    ts        INTEGER NOT NULL,
    avg       REAL NOT NULL,
    max       REAL NOT NULL,
    PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;

CREATE TABLE events (
    id       INTEGER PRIMARY KEY,
    ts       INTEGER NOT NULL,
    kind     TEXT NOT NULL,
    entity   TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info',
    detail   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_events_ts ON events (ts);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
