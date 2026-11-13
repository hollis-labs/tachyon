-- Per-session Continue toggle (CW-20261113-0019). continue_on=1 lets the session's plan follow to
-- the session that continues it (the Carry walk, 053); 0 stops the walk at this row before its
-- plan is taken, so the replacement starts fresh as if Tether had reported no continues. Unlike
-- cleared, nothing is deleted, so turning it back on restores continuity. New rows take the
-- "Default for Continue on new sessions" chat setting when first written; existing rows stay on.
-- Reading or setting the flag of a session not yet seen through the proxy writes its row with
-- agent_id '', which a walk treats like a clear marker until Carry names the agent.
ALTER TABLE chat_session_lineage ADD COLUMN continue_on INTEGER NOT NULL DEFAULT 1;
