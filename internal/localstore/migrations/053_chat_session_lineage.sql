-- Plan carry-over (CW-20261112-0056). One row per Tether session seen through the pinned proxy:
-- the logical agent it acts for and the session it continues (the relaunch parent, as Tether
-- states it). cleared=1 is the operator's "clear plan" / "new conversation" marker: a walk up the
-- chain never resurrects a plan from beyond it. A row with agent_id '' exists only for that marker.
CREATE TABLE chat_session_lineage (
 session_id TEXT PRIMARY KEY,
 agent_id TEXT NOT NULL,
 continues_id TEXT,
 cleared INTEGER NOT NULL DEFAULT 0,
 seen_ns INTEGER NOT NULL
);
