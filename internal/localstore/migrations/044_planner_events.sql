-- Planner events and stream (PL4). Additive.
-- planner_events is the timeline: one row per thing that happened to a theme, idempotent by
-- (source, source_event_id) so ingest can be replayed. planner_stream is the SSE ring with a
-- monotonic seq (the Last-Event-ID); planner_ingest_state holds ingest cursors and snapshots.
CREATE TABLE planner_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 event_id TEXT NOT NULL UNIQUE,
 at_ms INTEGER NOT NULL,
 theme_id TEXT NOT NULL DEFAULT '',
 subject_ref TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL CHECK(kind IN ('task','pr','decision','blocker','comment','workstream','lifecycle','link')),
 text TEXT NOT NULL,
 refs_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(refs_json)),
 delta_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(delta_json)),
 arrow TEXT NOT NULL DEFAULT 'flat' CHECK(arrow IN ('up','flat','down')),
 weight REAL NOT NULL DEFAULT 0,
 source TEXT NOT NULL,
 source_event_id TEXT NOT NULL,
 UNIQUE(source, source_event_id)
);
CREATE INDEX planner_events_theme ON planner_events(theme_id, seq DESC);
CREATE INDEX planner_events_subject ON planner_events(subject_ref, seq DESC);
CREATE TABLE planner_stream (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 event TEXT NOT NULL,
 data_json TEXT NOT NULL CHECK(json_valid(data_json)),
 at_ms INTEGER NOT NULL
);
CREATE TABLE planner_ingest_state (
 k TEXT PRIMARY KEY,
 v TEXT NOT NULL,
 updated_ms INTEGER NOT NULL
);
