-- Chat plans (T2). Additive. A plan is a Tachyon row shaped to Torque (statuses, task_ref,
-- project_id) so it can graduate; status sync from Torque is one-way. One plan per thread
-- (scope thread) and one per project (scope project); loose todos are a plan with no title.
CREATE TABLE chat_plans (
 id TEXT PRIMARY KEY,
 scope TEXT NOT NULL CHECK (scope IN ('thread','project')),
 thread_ref TEXT,
 project_id TEXT,
 title TEXT,
 status TEXT NOT NULL CHECK (status IN ('proposed','approved','active','done','cancelled')),
 revision INTEGER NOT NULL,
 task_ref TEXT,
 created_by TEXT NOT NULL,
 created_ns INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL,
 CHECK ((scope='thread' AND thread_ref IS NOT NULL AND project_id IS NULL) OR (scope='project' AND project_id IS NOT NULL AND thread_ref IS NULL))
);
CREATE UNIQUE INDEX chat_plans_thread ON chat_plans(thread_ref) WHERE scope='thread';
CREATE UNIQUE INDEX chat_plans_project ON chat_plans(project_id) WHERE scope='project';
CREATE TABLE chat_plan_steps (
 plan_id TEXT NOT NULL REFERENCES chat_plans(id) ON DELETE CASCADE,
 id TEXT NOT NULL,
 position INTEGER NOT NULL,
 title TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('todo','doing','blocked','done','cancelled')),
 acceptance TEXT NOT NULL DEFAULT '',
 task_ref TEXT,
 notes TEXT NOT NULL DEFAULT '',
 updated_ns INTEGER NOT NULL,
 PRIMARY KEY (plan_id, id)
);
CREATE INDEX chat_plan_steps_task ON chat_plan_steps(task_ref) WHERE task_ref IS NOT NULL;
-- App-agent messages gain a kind (text|card|plan|system) and a meta document.
ALTER TABLE app_agent_messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'text' CHECK (kind IN ('text','card','plan','system'));
ALTER TABLE app_agent_messages ADD COLUMN meta_json TEXT;
