-- Chat quick terminal (T8): interactive sessions, agent command proposals and their run records.
-- Output is never stored beyond a bounded, redacted tail; interactive sessions store byte counts only.

-- App-agent messages gain kind 'run' (a terminal run block). SQLite cannot widen a CHECK in place, so the
-- table is rebuilt; migrations run with foreign keys off.
CREATE TABLE app_agent_messages_new (
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
 kind TEXT NOT NULL DEFAULT 'text' CHECK (kind IN ('text','card','plan','system','run')),
 meta_json TEXT,
 UNIQUE (thread_id, seq)
);
INSERT INTO app_agent_messages_new(id,thread_id,seq,role,body,state,error,runtime_turn,tools,created_ns,updated_ns,kind,meta_json)
 SELECT id,thread_id,seq,role,body,state,error,runtime_turn,tools,created_ns,updated_ns,kind,meta_json FROM app_agent_messages;
DROP TABLE app_agent_messages;
ALTER TABLE app_agent_messages_new RENAME TO app_agent_messages;
CREATE INDEX app_agent_messages_thread ON app_agent_messages(thread_id, seq);
CREATE INDEX app_agent_messages_state ON app_agent_messages(state) WHERE state = 'streaming';

CREATE TABLE terminal_sessions (
 id TEXT PRIMARY KEY,
 thread_ref TEXT,
 machine_id TEXT NOT NULL,
 ssh_user TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK (state IN ('open','stale','closed')),
 opened_by TEXT NOT NULL,
 opened_ns INTEGER NOT NULL,
 ended_ns INTEGER,
 bytes_in INTEGER NOT NULL DEFAULT 0,
 bytes_out INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX terminal_sessions_state ON terminal_sessions(state);

CREATE TABLE terminal_runs (
 run_id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 machine_id TEXT NOT NULL,
 ssh_user TEXT NOT NULL DEFAULT '',
 command TEXT NOT NULL,
 op_digest TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 requested_by TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('pending_approval','running','finished','failed','stopped','denied','expired')),
 approval_mode TEXT NOT NULL CHECK (approval_mode IN ('user','policy')),
 rule_id TEXT,
 decided_by TEXT,
 decided_ns INTEGER,
 exit_code INTEGER,
 output_tail TEXT NOT NULL DEFAULT '',
 output_truncated INTEGER NOT NULL DEFAULT 0,
 close_on_complete INTEGER NOT NULL DEFAULT 0,
 closed_ns INTEGER,
 stopped_by TEXT,
 error TEXT NOT NULL DEFAULT '',
 message_ref TEXT,
 supersedes TEXT,
 started_ns INTEGER,
 ended_ns INTEGER,
 duration_ms INTEGER,
 created_ns INTEGER NOT NULL
);
CREATE INDEX terminal_runs_thread ON terminal_runs(thread_ref, created_ns);
CREATE INDEX terminal_runs_state ON terminal_runs(state);

-- A proposal is the approval record for one run: the exact bytes the operator saw are the ones that run.
CREATE TABLE terminal_proposals (
 id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL UNIQUE REFERENCES terminal_runs(run_id),
 thread_ref TEXT NOT NULL,
 session_id TEXT,
 machine_id TEXT NOT NULL,
 command TEXT NOT NULL,
 command_digest TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 requested_by TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('pending','approved','denied','superseded','expired')),
 item_id TEXT,
 created_ns INTEGER NOT NULL,
 expires_ns INTEGER NOT NULL,
 supersedes TEXT
);
CREATE INDEX terminal_proposals_thread ON terminal_proposals(thread_ref, state);
