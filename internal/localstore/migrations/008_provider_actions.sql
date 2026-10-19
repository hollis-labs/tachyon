CREATE TABLE provider_actions (
 id TEXT PRIMARY KEY,
 object_id TEXT NOT NULL REFERENCES objects(id),
 object_revision INTEGER NOT NULL,
 provider TEXT NOT NULL,
 target TEXT NOT NULL,
 credential_identity TEXT NOT NULL,
 verb TEXT NOT NULL,
 payload BLOB NOT NULL,
 digest TEXT NOT NULL,
 input_digest TEXT NOT NULL,
 state TEXT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 lease_until INTEGER NOT NULL DEFAULT 0,
 result BLOB NOT NULL DEFAULT '{}',
 problem TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX provider_actions_object ON provider_actions(object_id,id);
