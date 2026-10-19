-- Rebuild constrained tables using the migration runner's checked FK-safe
-- transaction. Existing source identities, revisions and relationships survive.
CREATE TABLE objects_next (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('agent','team','assignment','workflow','project','machine','repository','plan','task','epic','sprint','collection','workstream','session','run')),
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

CREATE TABLE execution_items (
 object_id TEXT PRIMARY KEY REFERENCES objects(id),
 kind TEXT NOT NULL CHECK(kind IN ('session','run')),
 status TEXT NOT NULL,
 project_id TEXT NOT NULL DEFAULT '',
 workstream_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL DEFAULT '',
 task_id TEXT NOT NULL DEFAULT '',
 run_id TEXT NOT NULL DEFAULT '',
 workflow_id TEXT NOT NULL DEFAULT '',
 started_at TEXT NOT NULL DEFAULT '',
 ended_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX execution_kind_status ON execution_items(kind,status,object_id);
CREATE INDEX execution_project ON execution_items(project_id,object_id);
CREATE INDEX execution_workstream ON execution_items(workstream_id,object_id);
CREATE INDEX execution_agent ON execution_items(agent_id,object_id);
CREATE INDEX execution_task ON execution_items(task_id,object_id);
CREATE INDEX execution_run ON execution_items(run_id,object_id);
CREATE INDEX execution_workflow ON execution_items(workflow_id,object_id);

-- Add associations without rewriting original chat bodies, session IDs or replies.
CREATE TABLE chat_session_links (
 turn_id TEXT PRIMARY KEY REFERENCES chat_turns(id),
 session_object_id TEXT NOT NULL REFERENCES objects(id),
 linked_at TEXT NOT NULL
);
CREATE INDEX chat_session_turns ON chat_session_links(session_object_id,turn_id);
CREATE TABLE chat_outbox_session_links (
 identity TEXT NOT NULL,
 id TEXT NOT NULL,
 session_object_id TEXT NOT NULL REFERENCES objects(id),
 linked_at TEXT NOT NULL,
 PRIMARY KEY(identity,id),
 FOREIGN KEY(identity,id) REFERENCES chat_outbox(identity,id)
);
CREATE INDEX chat_session_outbox ON chat_outbox_session_links(session_object_id,identity,id);
