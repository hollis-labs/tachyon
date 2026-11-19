-- How a restart brought the seat back, and what it lost (Tachyon restart fallback). Additive.
-- relaunched_by: 'resume' (POST /logical-agents/{id}/resume) or 'relaunch' (POST /sessions {continuity: relaunch}
-- when Tether refused the resume, e.g. a worker-placed seat). scopes_dropped: JSON array of the scopes Tether's
-- session.scopes_narrowed reported for the new session; NULL when it reported none.
ALTER TABLE agents_controls ADD COLUMN relaunched_by TEXT;
ALTER TABLE agents_controls ADD COLUMN scopes_dropped TEXT;
