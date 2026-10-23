-- One row per requested action, bound to its approval item and op_digest.
-- Terminal input is never stored; output is bounded and redacted.
CREATE TABLE action_runs (
 id TEXT PRIMARY KEY,
 item_id TEXT NOT NULL UNIQUE,
 machine TEXT NOT NULL,
 action TEXT NOT NULL,
 params TEXT NOT NULL,
 argv TEXT NOT NULL,
 op_digest TEXT NOT NULL,
 class TEXT NOT NULL,
 labels TEXT NOT NULL DEFAULT '',
 requested_by TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 run_user TEXT NOT NULL DEFAULT '',
 exit_code INTEGER,
 output TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 started_ns INTEGER,
 ended_ns INTEGER
);
CREATE INDEX action_runs_state ON action_runs(state,created_ns);
CREATE INDEX action_runs_created ON action_runs(created_ns DESC,id DESC);
