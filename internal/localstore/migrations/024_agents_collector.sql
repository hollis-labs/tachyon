-- Agents collector (A1). A derived, rebuildable read model of Tether's environment stream.
-- Additive. Only derived, allowlisted fields are stored: never raw model output or tool arguments.
CREATE TABLE agents_collector_state (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 env_id TEXT NOT NULL DEFAULT '',
 last_seq INTEGER NOT NULL DEFAULT 0,
 last_event_at_ns INTEGER NOT NULL DEFAULT 0,
 strategy TEXT NOT NULL DEFAULT '',
 started_ns INTEGER NOT NULL DEFAULT 0,
 epoch INTEGER NOT NULL DEFAULT 0,
 retention_last_ns INTEGER NOT NULL DEFAULT 0,
 retention_pruned INTEGER NOT NULL DEFAULT 0
);
INSERT INTO agents_collector_state(id) VALUES (1);
CREATE TABLE agents_gaps (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 start_ns INTEGER NOT NULL,
 end_ns INTEGER NOT NULL,
 reason TEXT NOT NULL,
 from_seq INTEGER NOT NULL DEFAULT 0,
 to_seq INTEGER NOT NULL DEFAULT 0,
 backfilled INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE agents_sessions (
 session_id TEXT PRIMARY KEY,
 session_state TEXT NOT NULL DEFAULT '',
 work_state TEXT NOT NULL DEFAULT '',
 work_known INTEGER NOT NULL DEFAULT 0,
 pending_approval INTEGER,
 pending_question INTEGER,
 provider TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 workroot TEXT NOT NULL DEFAULT '',
 agent_urn TEXT NOT NULL DEFAULT '',
 task_ref TEXT NOT NULL DEFAULT '',
 started_ns INTEGER NOT NULL DEFAULT 0,
 last_activity_ns INTEGER NOT NULL DEFAULT 0,
 ended_ns INTEGER NOT NULL DEFAULT 0,
 end_reason TEXT NOT NULL DEFAULT '',
 alive INTEGER,
 worker_online INTEGER,
 tool_calls INTEGER NOT NULL DEFAULT 0,
 updated_ns INTEGER NOT NULL
);
CREATE INDEX agents_sessions_activity ON agents_sessions(ended_ns, last_activity_ns);
CREATE TABLE agents_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL,
 tether_seq INTEGER NOT NULL,
 tether_event_id TEXT NOT NULL UNIQUE,
 type TEXT NOT NULL CHECK (type IN ('tool','message','approval','lifecycle','compaction','clear','error','task','control')),
 at_ns INTEGER NOT NULL,
 actor TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT ''
);
CREATE INDEX agents_events_session ON agents_events(session_id, tether_seq);
CREATE INDEX agents_events_at ON agents_events(at_ns);
