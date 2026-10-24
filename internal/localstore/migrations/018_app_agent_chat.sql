-- Tachyon-owned threads and messages for adopted app agents. Runtime-neutral:
-- the runtime (nanite today, embedded later) is data, never a schema concern.
-- Tachyon is the single writer; a runtime's own history is never imported.
CREATE TABLE app_agent_threads (
 id TEXT PRIMARY KEY,
 agent TEXT NOT NULL,
 title TEXT NOT NULL,
 owner TEXT NOT NULL DEFAULT 'tachyon',
 runtime TEXT NOT NULL DEFAULT 'nanite',
 runtime_session TEXT NOT NULL DEFAULT '',
 read_seq INTEGER NOT NULL DEFAULT 0,
 created_ns INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL
);
CREATE TABLE app_agent_messages (
 id TEXT PRIMARY KEY,
 thread_id TEXT NOT NULL REFERENCES app_agent_threads(id),
 seq INTEGER NOT NULL,
 role TEXT NOT NULL CHECK (role IN ('operator','agent')),
 body TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 runtime_turn TEXT NOT NULL DEFAULT '',
 tools TEXT NOT NULL DEFAULT '[]',
 created_ns INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL,
 UNIQUE (thread_id, seq)
);
CREATE INDEX app_agent_messages_thread ON app_agent_messages(thread_id, seq);
CREATE INDEX app_agent_messages_state ON app_agent_messages(state) WHERE state = 'streaming';
