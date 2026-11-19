-- Shared console item-state store (PR2). Additive. The one owner of /console/state: PR triage now,
-- Alerts compat on top (AL1). Keys carry a prefix (pr-inbox:, pr-assign:, ...); values hold no content.
CREATE TABLE console_item_state (
 key TEXT PRIMARY KEY,
 status TEXT NOT NULL CHECK (status IN ('unread','read','dismissed','snoozed','settled')),
 snooze_until_ms INTEGER,
 wake_at_ms INTEGER,
 note TEXT NOT NULL DEFAULT '',
 resolution TEXT NOT NULL DEFAULT '',
 by TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 1,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE INDEX console_item_state_snooze ON console_item_state(snooze_until_ms) WHERE status = 'snoozed';
