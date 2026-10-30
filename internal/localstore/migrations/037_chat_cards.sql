-- Chat cards (T5a). Additive. Binds an Approvals queue item to a chat thread so the card renders in the
-- thread stream and "need you" can be derived. The queue item stays the only record of the decision.
CREATE TABLE chat_card_refs (
 item_id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 message_ref TEXT,
 kind TEXT NOT NULL,
 created_ns INTEGER NOT NULL
);
CREATE INDEX chat_card_refs_thread ON chat_card_refs(thread_ref, created_ns);

-- "Always in this chat" rules (T5c). A rule only ever allows, and only low-class tool approvals; the matcher enforces that.
CREATE TABLE chat_approval_rules (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 agent_id TEXT NOT NULL DEFAULT '',
 tool TEXT NOT NULL,
 match_json TEXT NOT NULL DEFAULT '{}',
 expires_ns INTEGER,
 created_ns INTEGER NOT NULL,
 revoked_ns INTEGER
);
CREATE INDEX chat_approval_rules_thread ON chat_approval_rules(thread_ref, created_ns);
