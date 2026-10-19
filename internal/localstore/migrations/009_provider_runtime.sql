ALTER TABLE provider_actions ADD COLUMN origin_action_id TEXT NOT NULL DEFAULT '';
CREATE TABLE provider_roster_cache (
 identity TEXT NOT NULL,
 selection TEXT NOT NULL,
 body BLOB NOT NULL,
 observed_at TEXT NOT NULL,
 PRIMARY KEY(identity,selection)
);
CREATE INDEX provider_actions_created ON provider_actions(object_id,created_at DESC,id DESC);
CREATE INDEX provider_actions_verb_created ON provider_actions(object_id,verb,created_at DESC,id DESC);
