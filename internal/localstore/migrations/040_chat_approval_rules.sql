-- Chat approval rules (T5c). Additive.
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
