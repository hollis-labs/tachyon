ALTER TABLE provider_actions ADD COLUMN projection_problem TEXT NOT NULL DEFAULT '';
CREATE TABLE provider_run_links (
 formation_action_id TEXT PRIMARY KEY REFERENCES provider_actions(id),
 run_object_id TEXT NOT NULL UNIQUE REFERENCES objects(id),
 provider TEXT NOT NULL,
 target TEXT NOT NULL,
 external_run_id TEXT NOT NULL,
 team_id TEXT NOT NULL REFERENCES objects(id),
 team_revision INTEGER NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(provider,target,external_run_id)
);
CREATE INDEX provider_runs_team ON provider_run_links(team_id,created_at DESC,formation_action_id DESC);
CREATE INDEX provider_runs_created ON provider_run_links(created_at DESC,formation_action_id DESC);
CREATE INDEX provider_actions_origin_created ON provider_actions(origin_action_id,created_at DESC,id DESC);
CREATE INDEX provider_actions_assignment_origin ON provider_actions(object_id,verb,origin_action_id);
