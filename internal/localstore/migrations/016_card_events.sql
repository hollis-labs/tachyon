-- Outbox of card.* events for the hub. A row is created once per (item, kind)
-- with the event id minted up front, so a retry resends the same id and the
-- hub dedupes it. seq is the producer's own monotonic sequence.
CREATE TABLE card_event_outbox (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 item_id TEXT NOT NULL,
 kind TEXT NOT NULL,
 event_id TEXT NOT NULL,
 event TEXT NOT NULL,
 state TEXT NOT NULL,
 reject_code TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 sent_ns INTEGER,
 UNIQUE(item_id, kind)
);
CREATE INDEX card_event_outbox_pending ON card_event_outbox(state, seq);
-- Items whose events have all been queued, so the collector skips them.
CREATE TABLE card_event_scan (
 item_id TEXT PRIMARY KEY
);
