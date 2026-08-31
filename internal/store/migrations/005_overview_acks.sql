-- Overview "needs a look" acknowledgements: one suppressed (kind, entity)
-- concern with an expiry -- the insight_dismissals shape (identity tuple
-- plus until, pruned by Maintain once lapsed), for the FRAME-DERIVED
-- anomalies the Overview attention module surfaces (unhealthy container,
-- disk usage/errors, array stopped, critical source). Alert-backed
-- callouts never land here: acknowledging one IS an alert silence
-- (alert_silences), one mechanism per system.
--
-- kind and entity are both NOT NULL and always concrete -- unlike
-- alert_silences, where "" means "any", there is deliberately NO
-- global/wildcard ack shape at all: an ack only ever names one specific
-- concern, so a buggy or hand-rolled client can never quiet the whole
-- attention module with a single row (the all-empty-scope lesson
-- alert_silences had to add an explicit "scope":"all" gesture for).
CREATE TABLE overview_acks (
    id         INTEGER PRIMARY KEY,
    kind       TEXT NOT NULL,   -- anomaly kind: unhealthy|disk-usage|disk-errors|array-stopped|source-critical
    entity     TEXT NOT NULL,   -- container name / disk slot / 'array' / source name -- never ''
    until      INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
