-- One delivery of a terminal card to its asker's sink, per (item, revision).
-- The idempotency key is minted at claim time so a retry resends the same key.
CREATE TABLE card_deliveries (
 item_id TEXT NOT NULL,
 revision INTEGER NOT NULL,
 sink TEXT NOT NULL DEFAULT '',
 rule_id TEXT NOT NULL DEFAULT '',
 profile_hash TEXT NOT NULL DEFAULT '',
 completion TEXT NOT NULL DEFAULT '',
 delivery TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 idem_key TEXT NOT NULL,
 receipt TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_ns INTEGER NOT NULL DEFAULT 0,
 created_ns INTEGER NOT NULL,
 ended_ns INTEGER,
 PRIMARY KEY (item_id, revision)
);
CREATE INDEX card_deliveries_pending ON card_deliveries(state, next_ns);
