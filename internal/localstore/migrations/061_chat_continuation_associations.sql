-- A replacement has one exact committed continuation pair. It may later be a source.
-- The receipt distinguishes an eager commit from a lineage row written by a
-- proxied request, so a late eager commit can still reconcile the plan once.
CREATE TABLE chat_continuation_associations (
    replacement_session_id TEXT PRIMARY KEY,
    source_session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    plan_moved INTEGER NOT NULL CHECK (plan_moved IN (0, 1)),
    committed_ns INTEGER NOT NULL
);
