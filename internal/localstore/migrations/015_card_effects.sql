-- One row per approved card whose effects were considered. The row is claimed
-- (state=running) before any effect runs, so an effect set is never started
-- twice; a crash leaves it running and recovery records it interrupted.
CREATE TABLE card_effect_runs (
 item_id TEXT PRIMARY KEY,
 state TEXT NOT NULL,
 receipt TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 ended_ns INTEGER
);
