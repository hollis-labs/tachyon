-- Alerts store (AL1). Additive. Server-side alerts with stable ids and the operator's decisions
-- (status/read/note/snooze/defer/resolution), their related-event timeline, a monotonic change
-- sequence for the stream, and per-source resume cursors. Everything but the decision fields is
-- derived from ingested events.
CREATE TABLE alerts (
 alert_id TEXT PRIMARY KEY,
 seq INTEGER NOT NULL UNIQUE,
 kind TEXT NOT NULL,
 rule_id TEXT NOT NULL DEFAULT '',
 severity TEXT NOT NULL,
 severity_base TEXT NOT NULL,
 needs_you INTEGER NOT NULL DEFAULT 0,
 title TEXT NOT NULL,
 summary TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT '',
 icon TEXT NOT NULL DEFAULT 'info',
 status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','dismissed','snoozed','deferred','settled')),
 read INTEGER NOT NULL DEFAULT 0,
 count INTEGER NOT NULL DEFAULT 1,
 coalesce_key TEXT NOT NULL DEFAULT '',
 first_at_ms INTEGER NOT NULL,
 last_at_ms INTEGER NOT NULL,
 snooze_until_ms INTEGER,
 wake_at_ms INTEGER,
 defer_json TEXT,
 note TEXT NOT NULL DEFAULT '',
 resolution TEXT NOT NULL DEFAULT '',
 resolved_by TEXT NOT NULL DEFAULT '',
 primary_json TEXT,
 subjects_json TEXT NOT NULL DEFAULT '[]',
 escalate_at_ms INTEGER,
 escalated_at_ms INTEGER,
 version INTEGER NOT NULL DEFAULT 1,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE INDEX alerts_status ON alerts(status, needs_you, last_at_ms);
CREATE INDEX alerts_wake ON alerts(wake_at_ms) WHERE wake_at_ms IS NOT NULL;
CREATE INDEX alerts_coalesce ON alerts(coalesce_key, status);
CREATE TABLE alert_events (
 event_id INTEGER PRIMARY KEY AUTOINCREMENT,
 alert_id TEXT NOT NULL,
 dedupe_key TEXT NOT NULL UNIQUE,
 at_ms INTEGER NOT NULL,
 kind TEXT NOT NULL,
 label TEXT NOT NULL DEFAULT '',
 ref_json TEXT,
 correlation_id TEXT NOT NULL DEFAULT '',
 technical_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX alert_events_alert ON alert_events(alert_id, at_ms DESC);
CREATE TABLE alert_seq (id INTEGER PRIMARY KEY CHECK (id = 1), seq INTEGER NOT NULL);
INSERT INTO alert_seq(id, seq) VALUES (1, 0);
CREATE TABLE alert_source_cursor (
 source TEXT PRIMARY KEY,
 cursor TEXT NOT NULL,
 updated_ms INTEGER NOT NULL
);
