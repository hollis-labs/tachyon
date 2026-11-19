-- Chat threads (T1). Additive. chat_thread_state is the pin/archive/title overlay for
-- BOTH thread sources, keyed by the Tachyon-owned thread_ref (app:<id> | tether:<session>).
CREATE TABLE chat_thread_state (
 thread_ref TEXT PRIMARY KEY,
 pinned_ns INTEGER,
 archived_ns INTEGER,
 title_override TEXT,
 project_id TEXT,
 forked_from TEXT,
 read_ns INTEGER NOT NULL DEFAULT 0,
 updated_ns INTEGER NOT NULL
);
-- Replay log for activity-class SSE events: seq is the SSE id.
CREATE TABLE chat_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 thread_ref TEXT NOT NULL,
 type TEXT NOT NULL,
 body TEXT NOT NULL,
 at_ns INTEGER NOT NULL
);
CREATE INDEX chat_events_thread ON chat_events(thread_ref, seq);
-- POST messages idempotency, like the Tether outbox.
CREATE TABLE chat_message_keys (
 thread_ref TEXT NOT NULL,
 idempotency_key TEXT NOT NULL,
 message_id TEXT NOT NULL,
 reply_id TEXT NOT NULL,
 seq INTEGER NOT NULL,
 created_ns INTEGER NOT NULL,
 PRIMARY KEY (thread_ref, idempotency_key)
);
