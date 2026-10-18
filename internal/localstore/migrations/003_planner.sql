-- Extend the domain kind constraint while preserving IDs, history and links.
-- migrate runs table rebuilds with foreign keys disabled on its pinned connection,
-- checks every FK before commit, and restores enforcement before writers start.
CREATE TABLE objects_next (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('agent','team','assignment','workflow','project','machine','repository','plan','task')),
 ownership TEXT NOT NULL CHECK(ownership IN ('tachyon','source')),
 name TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0),
 local_json TEXT NOT NULL CHECK(json_valid(local_json) AND json_type(local_json)='object'),
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json) AND json_type(metadata_json)='object'),
 deleted_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO objects_next SELECT * FROM objects;
DROP TABLE objects;
ALTER TABLE objects_next RENAME TO objects;
CREATE INDEX objects_kind_name ON objects(kind,name,id);

-- Derived from the effective definition in the same transaction as its revision.
-- The object record remains the source of truth. This projection supports indexed
-- filtering without interpreting provider snapshots and sparse overlays on reads.
CREATE TABLE planner_items (
 object_id TEXT PRIMARY KEY REFERENCES objects(id),
 kind TEXT NOT NULL CHECK(kind IN ('plan','task')),
 status TEXT NOT NULL,
 priority INTEGER NOT NULL,
 project_id TEXT NOT NULL DEFAULT '',
 plan_id TEXT NOT NULL DEFAULT '',
 parent_id TEXT NOT NULL DEFAULT '',
 due_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX planner_kind_status ON planner_items(kind,status,object_id);
CREATE INDEX planner_project ON planner_items(project_id,object_id);
CREATE INDEX planner_plan ON planner_items(plan_id,object_id);
CREATE INDEX planner_parent ON planner_items(parent_id,object_id);
