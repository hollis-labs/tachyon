-- Synthetic Agent Ops rows for the TACHYON_E2E_AGENTS=backend run. e2e/serve.sh applies this to the run's throwaway
-- database after the schema exists, before serving. The collector is not started in this build, so these rows are all
-- the binary has. Times are relative to when the seed runs (now), so working and waiting sessions are not stale.
-- Names follow the contract stub's fixtures (e2e/agents-fixtures), so the same specs read both.

-- now and now-relative times, in ns
CREATE TEMP TABLE t AS SELECT CAST(strftime('%s', 'now') AS INTEGER) * 1000000000 AS now;

INSERT INTO agents_sessions (session_id, session_state, work_state, work_known, pending_approval, pending_question,
  provider, model, workroot, agent_urn, logical_agent_id, agent_name, started_ns, last_activity_ns, alive,
  worker_online, tool_calls, updated_ns)
SELECT 'sess_work', 'running', 'processing', 1, 0, 0, 'claude', 'claude-sonnet-5-5', '/work/tachyon',
  'msg://agent/agent-mux/agt_e2e_builder', 'tachyon-chat-backend-builder', 'Chat Backend Builder',
  now - 3600000000000, now - 30000000000, 1, 1, 12, now FROM t
UNION ALL
SELECT 'sess_approval', 'running', 'awaiting_approval', 1, 1, 0, 'claude', 'claude-sonnet-5-5', '/work/tachyon',
  'msg://agent/agent-mux/agt_e2e_review_bot', 'review-bot', 'Reviewer',
  now - 1800000000000, now - 60000000000, 1, 1, 4, now FROM t
UNION ALL
SELECT 'sess_idle', 'running', 'idle', 1, 0, 0, 'claude', 'claude-sonnet-5-5', '/work/nanite',
  'msg://agent/agent-mux/agt_e2e_architect', 'nanite-architect', 'Nanite Architect',
  now - 7200000000000, now - 600000000000, 1, 1, 0, now FROM t;

INSERT INTO agents_events (session_id, tether_seq, tether_event_id, type, at_ns, actor, summary)
SELECT 'sess_work', 1, 'e2e-ev-1', 'lifecycle', now - 3600000000000, 'tether', 'Session started (claude, claude-sonnet-5-5)' FROM t
UNION ALL SELECT 'sess_work', 2, 'e2e-ev-2', 'tool', now - 3000000000000, 'agent', 'Reading internal/appagent/' FROM t
UNION ALL SELECT 'sess_work', 3, 'e2e-ev-3', 'error', now - 1200000000000, 'agent', 'Tool failed: Bash (exit 1)' FROM t
UNION ALL SELECT 'sess_work', 4, 'e2e-ev-4', 'message', now - 60000000000, 'agent', 'Sent mail to portfolio-manager' FROM t;

-- Tachyon offline for five minutes in the middle of the session.
INSERT INTO agents_gaps (start_ns, end_ns, reason, from_seq, to_seq, backfilled)
SELECT now - 2400000000000, now - 2100000000000, 'tachyon_offline', 2, 3, 0 FROM t;

INSERT INTO seat_status (agent_urn, handle, state, reason, ref, owner, expires_at_ns, set_by, at_ns, tether_seq)
SELECT 'msg://agent/agent-mux/agt_e2e_builder', '@builder-1', 'blocked', 'needs the deploy token', 'task:CW-E2E-0001',
  '@chrispian', NULL, 'msg://agent/agent-mux/agt_e2e_builder', now - 600000000000, 1 FROM t
UNION ALL
SELECT 'msg://agent/agent-mux/agt_e2e_reviewer', '@reviewer', 'available', '', '', '', NULL,
  'msg://agent/agent-mux/agt_e2e_reviewer', now - 900000000000, 2 FROM t;
