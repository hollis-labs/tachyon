-- Planner task meta (PL3): what Tachyon records beside a Torque task. Additive.
-- done_summary is the "What shipped?" line; pr_cites_json is [{host,owner,repo,number,source}].
CREATE TABLE planner_task_meta (
 task_ref TEXT PRIMARY KEY,
 theme_id TEXT NOT NULL DEFAULT '',
 done_summary TEXT NOT NULL DEFAULT '',
 pr_cites_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(pr_cites_json)),
 summary_by TEXT NOT NULL DEFAULT '',
 summary_at TEXT NOT NULL DEFAULT '',
 updated_ms INTEGER NOT NULL
);
