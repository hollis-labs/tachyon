CREATE TABLE agent_launch_bindings (
 id TEXT PRIMARY KEY,
 agent_id TEXT NOT NULL REFERENCES objects(id),
 agent_revision INTEGER NOT NULL,
 target TEXT NOT NULL,
 credential_identity TEXT NOT NULL,
 input_digest TEXT NOT NULL,
 digest TEXT NOT NULL,
 evidence BLOB NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('prepared','active','revoked')),
 actor TEXT NOT NULL,
 confirmed_by TEXT NOT NULL DEFAULT '',
 revoked_by TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 confirmed_at TEXT NOT NULL DEFAULT '',
 revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX agent_launch_bindings_agent ON agent_launch_bindings(agent_id,created_at DESC,id DESC);
ALTER TABLE provider_actions ADD COLUMN binding_dependencies BLOB NOT NULL DEFAULT '[]';
