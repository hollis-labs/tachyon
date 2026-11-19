-- Agents controls (A6): one row per operator control request (nudge now; restart when Tether has the verb),
-- idempotent by request_id. The nudge text is not kept: it went to the agent as mail and Tether holds it.
CREATE TABLE agents_controls (
 request_id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('nudge','restart')),
 session_id TEXT NOT NULL,
 state TEXT NOT NULL,
 step TEXT,
 error TEXT,
 interrupt TEXT,
 message_id TEXT,
 handoff INTEGER NOT NULL DEFAULT 0,
 new_session_id TEXT,
 discarded_pending INTEGER,
 wait_ends_at INTEGER,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE INDEX agents_controls_session ON agents_controls(session_id, created_ms);
