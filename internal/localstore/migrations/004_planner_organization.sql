-- Rebuild constrained tables using the migration runner's checked FK-safe
-- transaction. Existing source identities, revisions and relationships survive.
CREATE TABLE objects_next (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('agent','team','assignment','workflow','project','machine','repository','plan','task','epic','sprint','collection','workstream')),
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

CREATE TABLE planner_items_next (
 object_id TEXT PRIMARY KEY REFERENCES objects(id),
 kind TEXT NOT NULL CHECK(kind IN ('plan','task','epic','sprint','collection','workstream')),
 status TEXT NOT NULL,
 priority INTEGER NOT NULL,
 project_id TEXT NOT NULL DEFAULT '',
 plan_id TEXT NOT NULL DEFAULT '',
 parent_id TEXT NOT NULL DEFAULT '',
 due_at TEXT NOT NULL DEFAULT '',
 epic_id TEXT NOT NULL DEFAULT '',
 sprint_id TEXT NOT NULL DEFAULT '',
 workstream_id TEXT NOT NULL DEFAULT '',
 starts_at TEXT NOT NULL DEFAULT '',
 ends_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO planner_items_next(object_id,kind,status,priority,project_id,plan_id,parent_id,due_at)
 SELECT object_id,kind,status,priority,project_id,plan_id,parent_id,due_at FROM planner_items;
DROP TABLE planner_items;
ALTER TABLE planner_items_next RENAME TO planner_items;
CREATE INDEX planner_kind_status ON planner_items(kind,status,object_id);
CREATE INDEX planner_project ON planner_items(project_id,object_id);
CREATE INDEX planner_plan ON planner_items(plan_id,object_id);
CREATE INDEX planner_parent ON planner_items(parent_id,object_id);
CREATE INDEX planner_epic ON planner_items(epic_id,object_id);
CREATE INDEX planner_sprint ON planner_items(sprint_id,object_id);
CREATE INDEX planner_workstream ON planner_items(workstream_id,object_id);
