-- Chat topics and bookmarks (T3). Additive. message_ref is the app message id or the
-- Tether turn id; seq is the message's position, stored so click-to-jump needs no lookup.
CREATE TABLE chat_topics (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 title TEXT NOT NULL,
 message_ref TEXT NOT NULL,
 seq INTEGER,
 added_by TEXT NOT NULL CHECK (added_by IN ('user','agent')),
 created_ns INTEGER NOT NULL
);
CREATE INDEX chat_topics_thread ON chat_topics(thread_ref, created_ns);
CREATE TABLE chat_bookmarks (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 message_ref TEXT NOT NULL,
 note TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 UNIQUE (thread_ref, message_ref)
);
