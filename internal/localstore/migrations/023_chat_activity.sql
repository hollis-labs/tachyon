-- Chat activity feed and context meter cache (T6). Additive. chat_activity is append-only; its
-- seq is the feed cursor (the SSE id of an `activity` event comes from chat_events, see T1).
CREATE TABLE chat_activity (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 thread_ref TEXT NOT NULL,
 type TEXT NOT NULL,
 actor TEXT NOT NULL,
 cite_json TEXT NOT NULL DEFAULT '[]',
 payload_json TEXT,
 source TEXT NOT NULL,
 at_ns INTEGER NOT NULL
);
CREATE INDEX chat_activity_thread ON chat_activity(thread_ref, seq);
CREATE TABLE chat_context (
 thread_ref TEXT PRIMARY KEY,
 used INTEGER,
 window INTEGER,
 model TEXT,
 at_ns INTEGER NOT NULL
);
