CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE api_cache (
  key TEXT PRIMARY KEY, scope TEXT NOT NULL, identity TEXT NOT NULL,
  body BLOB NOT NULL, content_type TEXT NOT NULL, updated_at INTEGER NOT NULL
 );
CREATE INDEX api_cache_age ON api_cache(updated_at);
CREATE TABLE chat_outbox (
 identity TEXT NOT NULL, id TEXT NOT NULL, launch_id TEXT NOT NULL, body TEXT NOT NULL,
 created_at INTEGER NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL, session_id TEXT NOT NULL,
 PRIMARY KEY(identity,id)
 );
CREATE INDEX chat_outbox_launch ON chat_outbox(identity,launch_id,created_at);
CREATE TABLE chat_turns (
 id TEXT PRIMARY KEY, source TEXT NOT NULL, channel TEXT NOT NULL, publication_id TEXT NOT NULL,
 launch_id TEXT NOT NULL, session_id TEXT NOT NULL, created_at TEXT NOT NULL,
 revision INTEGER NOT NULL, body BLOB NOT NULL, imported INTEGER NOT NULL DEFAULT 0
 );
CREATE UNIQUE INDEX chat_publication ON chat_turns(source,channel,publication_id) WHERE publication_id<>'';
CREATE TABLE chat_publications(source TEXT NOT NULL,channel TEXT NOT NULL,publication_id TEXT NOT NULL,sequence INTEGER NOT NULL,envelope BLOB NOT NULL,PRIMARY KEY(source,channel,publication_id));
CREATE TABLE chat_turn_revisions(turn_id TEXT NOT NULL,revision INTEGER NOT NULL,body BLOB NOT NULL,recorded_at TEXT NOT NULL,PRIMARY KEY(turn_id,revision));
CREATE TABLE chat_cursors(source TEXT NOT NULL,channel TEXT NOT NULL,cursor INTEGER NOT NULL,PRIMARY KEY(source,channel));
CREATE TABLE chat_sync_status(source TEXT PRIMARY KEY,updated_at TEXT NOT NULL,problem TEXT NOT NULL);
CREATE TABLE chat_reply_intents(turn_id TEXT PRIMARY KEY,request_key TEXT NOT NULL,body TEXT NOT NULL,reply_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);
CREATE TABLE chat_imports(source TEXT PRIMARY KEY,checksum TEXT NOT NULL,imported_at TEXT NOT NULL,turn_count INTEGER NOT NULL, snapshot BLOB NOT NULL);
CREATE TABLE tachyon_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')));
CREATE TABLE object_sources (
 object_id TEXT PRIMARY KEY REFERENCES objects(id),
 provider TEXT NOT NULL,
 instance TEXT NOT NULL,
 collection TEXT NOT NULL,
 kind TEXT NOT NULL,
 external_id TEXT NOT NULL,
 version TEXT NOT NULL,
 observed_at TEXT NOT NULL,
 deleted_at TEXT,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND json_type(snapshot_json)='object'),
 UNIQUE(provider,instance,collection,kind,external_id)
);
CREATE TABLE object_revisions (
 object_id TEXT NOT NULL REFERENCES objects(id),
 revision INTEGER NOT NULL,
 operation TEXT NOT NULL,
 actor TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 record_json TEXT NOT NULL CHECK(json_valid(record_json)),
 PRIMARY KEY(object_id,revision)
);
CREATE TABLE object_links (
 from_id TEXT NOT NULL REFERENCES objects(id),
 relation TEXT NOT NULL,
 slot TEXT NOT NULL DEFAULT '',
 to_id TEXT NOT NULL REFERENCES objects(id),
 PRIMARY KEY(from_id,relation,slot,to_id)
);
CREATE INDEX object_links_target ON object_links(to_id,relation,from_id);
CREATE TABLE object_sync_watermarks (
 provider TEXT NOT NULL, instance TEXT NOT NULL, collection TEXT NOT NULL,
 kind TEXT NOT NULL, observed_at TEXT NOT NULL,
 PRIMARY KEY(provider,instance,collection,kind)
);
CREATE TABLE object_sync_state (
 source_id TEXT PRIMARY KEY,
 scope_json TEXT NOT NULL,
 run_id TEXT NOT NULL DEFAULT '',
 lease_until TEXT NOT NULL DEFAULT '',
 phase TEXT NOT NULL DEFAULT 'idle',
 started_at TEXT NOT NULL DEFAULT '',
 finished_at TEXT NOT NULL DEFAULT '',
 last_success_at TEXT NOT NULL DEFAULT '',
 pages INTEGER NOT NULL DEFAULT 0,
 items INTEGER NOT NULL DEFAULT 0,
 checkpoint TEXT NOT NULL DEFAULT '',
 authoritative INTEGER NOT NULL DEFAULT 0,
 problem TEXT NOT NULL DEFAULT ''
);
CREATE TABLE object_sync_runs (
 id TEXT PRIMARY KEY,
 source_id TEXT NOT NULL,
 fire_id TEXT NOT NULL DEFAULT '',
 started_at TEXT NOT NULL,
 finished_at TEXT NOT NULL DEFAULT '',
 phase TEXT NOT NULL,
 pages INTEGER NOT NULL DEFAULT 0,
 items INTEGER NOT NULL DEFAULT 0,
 authoritative INTEGER NOT NULL DEFAULT 0,
 problem TEXT NOT NULL DEFAULT ''
);
CREATE INDEX object_sync_runs_source ON object_sync_runs(source_id,started_at);
CREATE INDEX object_sync_runs_fire ON object_sync_runs(fire_id,phase);
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
CREATE TABLE IF NOT EXISTS "objects" (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('agent','team','assignment','workflow','project','machine','repository','plan','task','epic','sprint','collection','workstream','session','run','inbox_item','idea','decision','risk','roadmap_item','strategic_priority')),
 ownership TEXT NOT NULL CHECK(ownership IN ('tachyon','source')),
 name TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0),
 local_json TEXT NOT NULL CHECK(json_valid(local_json) AND json_type(local_json)='object'),
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json) AND json_type(metadata_json)='object'),
 deleted_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX objects_kind_name ON objects(kind,name,id);
CREATE TABLE IF NOT EXISTS "planner_items" (
 object_id TEXT PRIMARY KEY REFERENCES objects(id),
 kind TEXT NOT NULL CHECK(kind IN ('plan','task','epic','sprint','collection','workstream','inbox_item','idea','decision','risk','roadmap_item','strategic_priority')),
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
CREATE INDEX planner_kind_status ON planner_items(kind,status,object_id);
CREATE INDEX planner_project ON planner_items(project_id,object_id);
CREATE INDEX planner_plan ON planner_items(plan_id,object_id);
CREATE INDEX planner_parent ON planner_items(parent_id,object_id);
CREATE INDEX planner_epic ON planner_items(epic_id,object_id);
CREATE INDEX planner_sprint ON planner_items(sprint_id,object_id);
CREATE INDEX planner_workstream ON planner_items(workstream_id,object_id);
CREATE TABLE planner_promotions (
 source_id TEXT PRIMARY KEY REFERENCES objects(id),
 target_id TEXT NOT NULL UNIQUE REFERENCES objects(id),
 source_revision INTEGER NOT NULL,
 target_revision INTEGER NOT NULL,
 request_json TEXT NOT NULL CHECK(json_valid(request_json)),
 actor TEXT NOT NULL,
 created_at TEXT NOT NULL,
 FOREIGN KEY(source_id,source_revision) REFERENCES object_revisions(object_id,revision),
 FOREIGN KEY(target_id,target_revision) REFERENCES object_revisions(object_id,revision),
 CHECK(source_id <> target_id)
);
CREATE TABLE service_identities (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 disabled_at INTEGER
);
CREATE TABLE service_tokens (
 id TEXT PRIMARY KEY,
 identity_id TEXT NOT NULL REFERENCES service_identities(id),
 token_hash TEXT NOT NULL UNIQUE,
 grants TEXT NOT NULL CHECK(json_valid(grants)),
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 revoked_at INTEGER,
 CHECK(expires_at > created_at)
);
CREATE INDEX service_tokens_identity ON service_tokens(identity_id);
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
, origin_action_id TEXT NOT NULL DEFAULT '', projection_problem TEXT NOT NULL DEFAULT '', binding_dependencies BLOB NOT NULL DEFAULT '[]');
CREATE INDEX provider_actions_object ON provider_actions(object_id,id);
CREATE TABLE provider_roster_cache (
 identity TEXT NOT NULL,
 selection TEXT NOT NULL,
 body BLOB NOT NULL,
 observed_at TEXT NOT NULL,
 PRIMARY KEY(identity,selection)
);
CREATE INDEX provider_actions_created ON provider_actions(object_id,created_at DESC,id DESC);
CREATE INDEX provider_actions_verb_created ON provider_actions(object_id,verb,created_at DESC,id DESC);
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
CREATE TABLE approval_items (
 id TEXT PRIMARY KEY,
 caller_scope TEXT NOT NULL,
 idempotency_key TEXT NOT NULL,
 state TEXT NOT NULL,
 revision INTEGER NOT NULL,
 terminal INTEGER NOT NULL,
 expires_ns INTEGER,
 created_ns INTEGER NOT NULL,
 record BLOB NOT NULL,
 UNIQUE(caller_scope,idempotency_key)
);
CREATE INDEX approval_items_expiry ON approval_items(terminal,expires_ns,id);
CREATE INDEX approval_items_created ON approval_items(created_ns DESC,id DESC);
CREATE TABLE approval_proof_receipts (
 authority TEXT NOT NULL,
 proof_id TEXT NOT NULL,
 completion_id TEXT NOT NULL,
 item_id TEXT NOT NULL UNIQUE REFERENCES approval_items(id),
 expected_revision INTEGER NOT NULL,
 completion_digest TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 PRIMARY KEY(authority, proof_id),
 UNIQUE(authority, completion_id)
);
CREATE TABLE action_runs (
 id TEXT PRIMARY KEY,
 item_id TEXT NOT NULL UNIQUE,
 machine TEXT NOT NULL,
 action TEXT NOT NULL,
 params TEXT NOT NULL,
 argv TEXT NOT NULL,
 op_digest TEXT NOT NULL,
 class TEXT NOT NULL,
 labels TEXT NOT NULL DEFAULT '',
 requested_by TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 run_user TEXT NOT NULL DEFAULT '',
 exit_code INTEGER,
 output TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 started_ns INTEGER,
 ended_ns INTEGER
);
CREATE INDEX action_runs_state ON action_runs(state,created_ns);
CREATE INDEX action_runs_created ON action_runs(created_ns DESC,id DESC);
CREATE TABLE card_effect_runs (
 item_id TEXT PRIMARY KEY,
 state TEXT NOT NULL,
 receipt TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 ended_ns INTEGER
);
CREATE TABLE card_event_outbox (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 item_id TEXT NOT NULL,
 kind TEXT NOT NULL,
 event_id TEXT NOT NULL,
 event TEXT NOT NULL,
 state TEXT NOT NULL,
 reject_code TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 sent_ns INTEGER,
 UNIQUE(item_id, kind)
);
CREATE TABLE sqlite_sequence(name,seq);
CREATE INDEX card_event_outbox_pending ON card_event_outbox(state, seq);
CREATE TABLE card_event_scan (
 item_id TEXT PRIMARY KEY
);
CREATE TABLE card_deliveries (
 item_id TEXT NOT NULL,
 revision INTEGER NOT NULL,
 sink TEXT NOT NULL DEFAULT '',
 rule_id TEXT NOT NULL DEFAULT '',
 profile_hash TEXT NOT NULL DEFAULT '',
 completion TEXT NOT NULL DEFAULT '',
 delivery TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 idem_key TEXT NOT NULL,
 receipt TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_ns INTEGER NOT NULL DEFAULT 0,
 created_ns INTEGER NOT NULL,
 ended_ns INTEGER,
 PRIMARY KEY (item_id, revision)
);
CREATE INDEX card_deliveries_pending ON card_deliveries(state, next_ns);
CREATE TABLE app_agent_threads (
 id TEXT PRIMARY KEY,
 agent TEXT NOT NULL,
 title TEXT NOT NULL,
 owner TEXT NOT NULL DEFAULT 'tachyon',
 runtime TEXT NOT NULL DEFAULT 'nanite',
 runtime_session TEXT NOT NULL DEFAULT '',
 read_seq INTEGER NOT NULL DEFAULT 0,
 created_ns INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL
);
CREATE TABLE chat_thread_state (
 thread_ref TEXT PRIMARY KEY,
 pinned_ns INTEGER,
 archived_ns INTEGER,
 title_override TEXT,
 project_id TEXT,
 forked_from TEXT,
 read_ns INTEGER NOT NULL DEFAULT 0,
 updated_ns INTEGER NOT NULL
);
CREATE TABLE chat_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 thread_ref TEXT NOT NULL,
 type TEXT NOT NULL,
 body TEXT NOT NULL,
 at_ns INTEGER NOT NULL
);
CREATE INDEX chat_events_thread ON chat_events(thread_ref, seq);
CREATE TABLE chat_message_keys (
 thread_ref TEXT NOT NULL,
 idempotency_key TEXT NOT NULL,
 message_id TEXT NOT NULL,
 reply_id TEXT NOT NULL,
 seq INTEGER NOT NULL,
 created_ns INTEGER NOT NULL,
 PRIMARY KEY (thread_ref, idempotency_key)
);
CREATE TABLE chat_workspace_items (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 type TEXT NOT NULL CHECK (type IN ('file','link','ref','image')),
 name TEXT NOT NULL,
 mime TEXT,
 size_bytes INTEGER,
 sha256 TEXT,
 url TEXT,
 ref TEXT,
 shared INTEGER NOT NULL DEFAULT 0,
 added_by TEXT NOT NULL,
 created_ns INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL
);
CREATE INDEX chat_workspace_thread ON chat_workspace_items(thread_ref, created_ns);
CREATE TABLE chat_scratchpads (
 thread_ref TEXT PRIMARY KEY,
 body TEXT NOT NULL,
 revision INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL
);
CREATE TABLE chat_topics (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 title TEXT NOT NULL,
 message_ref TEXT NOT NULL,
 seq INTEGER,
 added_by TEXT NOT NULL CHECK (added_by IN ('user','agent')),
 created_ns INTEGER NOT NULL
);
CREATE INDEX chat_topics_thread ON chat_topics(thread_ref, created_ns);
CREATE TABLE chat_bookmarks (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 message_ref TEXT NOT NULL,
 note TEXT NOT NULL DEFAULT '',
 created_ns INTEGER NOT NULL,
 UNIQUE (thread_ref, message_ref)
);
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
 updated_ns INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 1,
 CHECK ((scope='thread' AND thread_ref IS NOT NULL AND project_id IS NULL) OR (scope='project' AND project_id IS NOT NULL AND thread_ref IS NULL))
);
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
CREATE TABLE chat_activity (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 thread_ref TEXT NOT NULL,
 type TEXT NOT NULL,
 actor TEXT NOT NULL,
 cite_json TEXT NOT NULL DEFAULT '[]',
 payload_json TEXT,
 source TEXT NOT NULL,
 at_ns INTEGER NOT NULL
);
CREATE INDEX chat_activity_thread ON chat_activity(thread_ref, seq);
CREATE TABLE chat_context (
 thread_ref TEXT PRIMARY KEY,
 used INTEGER,
 window INTEGER,
 model TEXT,
 at_ns INTEGER NOT NULL
);
CREATE TABLE agents_collector_state (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 env_id TEXT NOT NULL DEFAULT '',
 last_seq INTEGER NOT NULL DEFAULT 0,
 last_event_at_ns INTEGER NOT NULL DEFAULT 0,
 strategy TEXT NOT NULL DEFAULT '',
 started_ns INTEGER NOT NULL DEFAULT 0,
 epoch INTEGER NOT NULL DEFAULT 0,
 retention_last_ns INTEGER NOT NULL DEFAULT 0,
 retention_pruned INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE agents_gaps (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 start_ns INTEGER NOT NULL,
 end_ns INTEGER NOT NULL,
 reason TEXT NOT NULL,
 from_seq INTEGER NOT NULL DEFAULT 0,
 to_seq INTEGER NOT NULL DEFAULT 0,
 backfilled INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE agents_sessions (
 session_id TEXT PRIMARY KEY,
 session_state TEXT NOT NULL DEFAULT '',
 work_state TEXT NOT NULL DEFAULT '',
 work_known INTEGER NOT NULL DEFAULT 0,
 pending_approval INTEGER,
 pending_question INTEGER,
 provider TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 workroot TEXT NOT NULL DEFAULT '',
 agent_urn TEXT NOT NULL DEFAULT '',
 task_ref TEXT NOT NULL DEFAULT '',
 started_ns INTEGER NOT NULL DEFAULT 0,
 last_activity_ns INTEGER NOT NULL DEFAULT 0,
 ended_ns INTEGER NOT NULL DEFAULT 0,
 end_reason TEXT NOT NULL DEFAULT '',
 alive INTEGER,
 worker_online INTEGER,
 tool_calls INTEGER NOT NULL DEFAULT 0,
 updated_ns INTEGER NOT NULL
, one_liner_json TEXT, calls_this_turn INTEGER NOT NULL DEFAULT 0, ctx_used INTEGER, ctx_window INTEGER, ctx_model TEXT, compactions_json TEXT NOT NULL DEFAULT '[]', last_clear_ns INTEGER NOT NULL DEFAULT 0, last_clear_inferred INTEGER NOT NULL DEFAULT 0, tok_in INTEGER, tok_out INTEGER, tok_cr INTEGER, tok_cw INTEGER, logical_agent_id TEXT NOT NULL DEFAULT '', agent_name TEXT NOT NULL DEFAULT '', parent_session_id TEXT NOT NULL DEFAULT '', replaced_by TEXT NOT NULL DEFAULT '');
CREATE INDEX agents_sessions_activity ON agents_sessions(ended_ns, last_activity_ns);
CREATE TABLE agents_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL,
 tether_seq INTEGER NOT NULL,
 tether_event_id TEXT NOT NULL UNIQUE,
 type TEXT NOT NULL CHECK (type IN ('tool','message','approval','lifecycle','compaction','clear','error','task','control')),
 at_ns INTEGER NOT NULL,
 actor TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT ''
, tether_summary TEXT NOT NULL DEFAULT '', cell TEXT NOT NULL DEFAULT '');
CREATE INDEX agents_events_session ON agents_events(session_id, tether_seq);
CREATE INDEX agents_events_at ON agents_events(at_ns);
CREATE TABLE agents_samples (
 session_id TEXT NOT NULL,
 bucket_ns INTEGER NOT NULL,
 bucket_kind TEXT NOT NULL CHECK (bucket_kind IN ('min','hour')),
 activity INTEGER NOT NULL DEFAULT 0,
 ctx_used INTEGER,
 ctx_window INTEGER,
 tok_in INTEGER NOT NULL DEFAULT 0,
 tok_out INTEGER NOT NULL DEFAULT 0,
 tok_cr INTEGER NOT NULL DEFAULT 0,
 tok_cw INTEGER NOT NULL DEFAULT 0,
 tool_calls INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (session_id, bucket_kind, bucket_ns)
);
CREATE TABLE agents_stream (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK (type IN ('bus','control','gap')),
 at_ns INTEGER NOT NULL,
 data_json TEXT NOT NULL
);
CREATE INDEX agents_stream_at ON agents_stream(at_ns);
CREATE TABLE approval_item_index (
    item_id           TEXT PRIMARY KEY REFERENCES approval_items(id),
    revision          INTEGER NOT NULL,
    state             TEXT NOT NULL,
    terminal          INTEGER NOT NULL,
    status            TEXT NOT NULL,
    kind              TEXT NOT NULL,
    grp               TEXT NOT NULL,
    risk              TEXT NOT NULL,
    risk_rank         INTEGER NOT NULL,
    title             TEXT NOT NULL,
    summary           TEXT NOT NULL,
    requester_id      TEXT NOT NULL,
    requester_label   TEXT NOT NULL,
    requester_kind    TEXT NOT NULL,
    session_ref       TEXT NOT NULL,
    created_ns        INTEGER NOT NULL,
    expires_ns        INTEGER,
    decided_ns        INTEGER,
    deferred_until_ns INTEGER,
    interactive       INTEGER NOT NULL,
    has_secret        INTEGER NOT NULL,
    command_exec      INTEGER NOT NULL,
    destructive       INTEGER NOT NULL,
    effect_ops        TEXT NOT NULL,
    search            TEXT NOT NULL
);
CREATE INDEX approval_item_index_pending ON approval_item_index(terminal, grp, risk_rank);
CREATE INDEX approval_item_index_history ON approval_item_index(terminal, status, created_ns DESC, item_id DESC);
CREATE INDEX approval_item_index_requester ON approval_item_index(requester_id);
CREATE TABLE approval_defers (
    item_id     TEXT PRIMARY KEY REFERENCES approval_items(id),
    until_ns    INTEGER NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    deferred_by TEXT NOT NULL DEFAULT '',
    created_ns  INTEGER NOT NULL
);
CREATE TABLE approval_decision_log (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    TEXT NOT NULL UNIQUE,
    ts_ns       INTEGER NOT NULL,
    item_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT '',
    actor_ref   TEXT NOT NULL DEFAULT '',
    assurance   TEXT NOT NULL DEFAULT '',
    decision    TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    reason_code TEXT NOT NULL DEFAULT '',
    batch_id    TEXT NOT NULL DEFAULT '',
    revision    INTEGER NOT NULL DEFAULT 0,
    extra       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX approval_decision_log_item ON approval_decision_log(item_id, seq);
CREATE INDEX approval_decision_log_kind ON approval_decision_log(kind, status, seq DESC);
CREATE TRIGGER approval_decision_log_no_update BEFORE UPDATE ON approval_decision_log
BEGIN SELECT RAISE(ABORT, 'approval_decision_log is append-only'); END;
CREATE TRIGGER approval_decision_log_no_delete BEFORE DELETE ON approval_decision_log
BEGIN SELECT RAISE(ABORT, 'approval_decision_log is append-only'); END;
CREATE TABLE catalog_entities (
 path TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('agent','launch','project','skill','mcp_server','provider','other')),
 id TEXT NOT NULL,
 layer TEXT NOT NULL DEFAULT '',
 sha TEXT NOT NULL DEFAULT '',
 fingerprint TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','disabled','retired')),
 provider TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 fields_json TEXT NOT NULL DEFAULT '{}',
 updated_ns INTEGER NOT NULL
);
CREATE INDEX catalog_entities_kind ON catalog_entities(kind, id);
CREATE TABLE catalog_snapshots (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 path TEXT NOT NULL,
 sha TEXT NOT NULL,
 content TEXT NOT NULL,
 at_ns INTEGER NOT NULL,
 source TEXT NOT NULL CHECK (source IN ('builder','observed')),
 plan_id TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT ''
);
CREATE INDEX catalog_snapshots_path ON catalog_snapshots(path, id DESC);
CREATE TABLE catalog_state (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 observed_ns INTEGER NOT NULL DEFAULT 0,
 source_status TEXT NOT NULL DEFAULT 'starting',
 last_error TEXT NOT NULL DEFAULT '',
 last_plan_id TEXT NOT NULL DEFAULT '',
 full_scan_ns INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE console_item_state (
 key TEXT PRIMARY KEY,
 status TEXT NOT NULL CHECK (status IN ('unread','read','dismissed','snoozed','settled')),
 snooze_until_ms INTEGER,
 wake_at_ms INTEGER,
 note TEXT NOT NULL DEFAULT '',
 resolution TEXT NOT NULL DEFAULT '',
 by TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 1,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE INDEX console_item_state_snooze ON console_item_state(snooze_until_ms) WHERE status = 'snoozed';
CREATE TABLE alerts (
 alert_id TEXT PRIMARY KEY,
 seq INTEGER NOT NULL UNIQUE,
 kind TEXT NOT NULL,
 rule_id TEXT NOT NULL DEFAULT '',
 severity TEXT NOT NULL,
 severity_base TEXT NOT NULL,
 needs_you INTEGER NOT NULL DEFAULT 0,
 title TEXT NOT NULL,
 summary TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT '',
 icon TEXT NOT NULL DEFAULT 'info',
 status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','dismissed','snoozed','deferred','settled')),
 read INTEGER NOT NULL DEFAULT 0,
 count INTEGER NOT NULL DEFAULT 1,
 coalesce_key TEXT NOT NULL DEFAULT '',
 first_at_ms INTEGER NOT NULL,
 last_at_ms INTEGER NOT NULL,
 snooze_until_ms INTEGER,
 wake_at_ms INTEGER,
 defer_json TEXT,
 note TEXT NOT NULL DEFAULT '',
 resolution TEXT NOT NULL DEFAULT '',
 resolved_by TEXT NOT NULL DEFAULT '',
 primary_json TEXT,
 subjects_json TEXT NOT NULL DEFAULT '[]',
 escalate_at_ms INTEGER,
 escalated_at_ms INTEGER,
 version INTEGER NOT NULL DEFAULT 1,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE INDEX alerts_status ON alerts(status, needs_you, last_at_ms);
CREATE INDEX alerts_wake ON alerts(wake_at_ms) WHERE wake_at_ms IS NOT NULL;
CREATE INDEX alerts_coalesce ON alerts(coalesce_key, status);
CREATE TABLE alert_events (
 event_id INTEGER PRIMARY KEY AUTOINCREMENT,
 alert_id TEXT NOT NULL,
 dedupe_key TEXT NOT NULL UNIQUE,
 at_ms INTEGER NOT NULL,
 kind TEXT NOT NULL,
 label TEXT NOT NULL DEFAULT '',
 ref_json TEXT,
 correlation_id TEXT NOT NULL DEFAULT '',
 technical_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX alert_events_alert ON alert_events(alert_id, at_ms DESC);
CREATE TABLE alert_seq (id INTEGER PRIMARY KEY CHECK (id = 1), seq INTEGER NOT NULL);
CREATE TABLE alert_source_cursor (
 source TEXT PRIMARY KEY,
 cursor TEXT NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE TABLE fileview_audit (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 ts_ms INTEGER NOT NULL,
 principal TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL CHECK (kind IN ('stat','open','frame','caster')),
 source TEXT NOT NULL CHECK (source IN ('host','caster')),
 path_or_ref TEXT NOT NULL DEFAULT '',
 resolved_root TEXT,
 outcome TEXT NOT NULL CHECK (outcome IN ('ok','denied','error')),
 deny_reason TEXT,
 bytes_served INTEGER,
 etag TEXT,
 count INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX fileview_audit_ts ON fileview_audit(ts_ms);
CREATE TABLE approval_secrets (
    id           TEXT PRIMARY KEY,
    item_id      TEXT NOT NULL REFERENCES approval_items(id),
    field        TEXT NOT NULL,
    nonce        BLOB NOT NULL,
    ciphertext   BLOB NOT NULL,
    size         INTEGER NOT NULL,
    fingerprint  TEXT NOT NULL,
    created_ns   INTEGER NOT NULL,
    expires_ns   INTEGER NOT NULL,
    consumed_ns  INTEGER,
    session_ref  TEXT NOT NULL DEFAULT '',
    idem_key     TEXT NOT NULL DEFAULT '',
    idem_mac     BLOB
);
CREATE INDEX approval_secrets_item ON approval_secrets(item_id, field);
CREATE UNIQUE INDEX approval_secrets_idem ON approval_secrets(session_ref, idem_key) WHERE idem_key <> '';
CREATE TABLE forge_pr_index (
    pr_key      TEXT PRIMARY KEY,
    repo        TEXT NOT NULL,
    number      INTEGER NOT NULL,
    updated_at  TEXT NOT NULL,
    head_sha    TEXT NOT NULL DEFAULT '',
    pending     INTEGER NOT NULL DEFAULT 0,
    row_json    TEXT NOT NULL,
    fetched_ms  INTEGER NOT NULL
);
CREATE INDEX forge_pr_index_repo ON forge_pr_index(repo);
CREATE TABLE forge_index_state (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
);
CREATE TABLE approval_batches (
    batch_id       TEXT PRIMARY KEY,
    request_digest TEXT NOT NULL,
    response       TEXT NOT NULL,
    created_ms     INTEGER NOT NULL
);
CREATE INDEX approval_batches_created ON approval_batches(created_ms);
CREATE TABLE chat_card_refs (
 item_id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 message_ref TEXT,
 kind TEXT NOT NULL,
 created_ns INTEGER NOT NULL
);
CREATE INDEX chat_card_refs_thread ON chat_card_refs(thread_ref, created_ns);
CREATE TABLE catalog_changes (
    change_id  TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    operator   TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL,
    request    TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_ns INTEGER NOT NULL,
    updated_ns INTEGER NOT NULL
);
CREATE INDEX catalog_changes_state ON catalog_changes(state, updated_ns);
CREATE INDEX catalog_changes_operator ON catalog_changes(operator, created_ns);
CREATE TABLE catalog_change_paths (
    path      TEXT PRIMARY KEY,
    change_id TEXT NOT NULL REFERENCES catalog_changes(change_id)
);
CREATE TABLE library_parts (
    kind       TEXT NOT NULL CHECK (kind IN ('prompt','policy')),
    id         TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    summary    TEXT NOT NULL DEFAULT '',
    body_json  TEXT NOT NULL,
    revision   INTEGER NOT NULL,
    archived   INTEGER NOT NULL DEFAULT 0,
    created_ns INTEGER NOT NULL,
    updated_ns INTEGER NOT NULL,
    PRIMARY KEY (kind, id)
);
CREATE TABLE library_part_revisions (
    kind      TEXT NOT NULL,
    id        TEXT NOT NULL,
    revision  INTEGER NOT NULL,
    body_json TEXT NOT NULL,
    actor     TEXT NOT NULL DEFAULT '',
    plan_id   TEXT NOT NULL DEFAULT '',
    at_ns     INTEGER NOT NULL,
    PRIMARY KEY (kind, id, revision)
);
CREATE TABLE part_refs (
    agent_id      TEXT NOT NULL,
    kind          TEXT NOT NULL,
    part_id       TEXT NOT NULL,
    part_revision INTEGER NOT NULL,
    compiled_hash TEXT NOT NULL,
    plan_id       TEXT NOT NULL DEFAULT '',
    applied_ns    INTEGER NOT NULL,
    PRIMARY KEY (agent_id, kind, part_id)
);
CREATE INDEX part_refs_part ON part_refs(kind, part_id);
CREATE TABLE chat_approval_rules (
 id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 agent_id TEXT NOT NULL DEFAULT '',
 tool TEXT NOT NULL,
 match_json TEXT NOT NULL DEFAULT '{}',
 expires_ns INTEGER,
 created_ns INTEGER NOT NULL,
 revoked_ns INTEGER
);
CREATE INDEX chat_approval_rules_thread ON chat_approval_rules(thread_ref, created_ns);
CREATE TABLE planner_relation_index (
 from_id TEXT NOT NULL REFERENCES objects(id),
 kind TEXT NOT NULL CHECK(kind IN ('needs','delivers','mitigates','relates')),
 to_id TEXT NOT NULL,
 from_db TEXT NOT NULL,
 to_db TEXT NOT NULL,
 PRIMARY KEY(from_id, kind, to_id)
);
CREATE INDEX planner_relation_index_to ON planner_relation_index(to_id, kind);
CREATE TABLE planner_task_meta (
 task_ref TEXT PRIMARY KEY,
 theme_id TEXT NOT NULL DEFAULT '',
 done_summary TEXT NOT NULL DEFAULT '',
 pr_cites_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(pr_cites_json)),
 summary_by TEXT NOT NULL DEFAULT '',
 summary_at TEXT NOT NULL DEFAULT '',
 updated_ms INTEGER NOT NULL
);
CREATE TABLE transcript_snapshot (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 body BLOB NOT NULL,
 observed_ms INTEGER NOT NULL
);
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
CREATE TABLE forge_idempotency (
    key             TEXT NOT NULL,
    op              TEXT NOT NULL,
    request_digest  TEXT NOT NULL,
    response_status INTEGER NOT NULL,
    response_json   TEXT NOT NULL,
    created_ms      INTEGER NOT NULL,
    PRIMARY KEY (key, op)
);
CREATE INDEX forge_idempotency_created ON forge_idempotency(created_ms);
CREATE TABLE forge_review_drafts (
    pr_key     TEXT NOT NULL,
    principal  TEXT NOT NULL,
    head_sha   TEXT NOT NULL,
    body_json  TEXT NOT NULL,
    updated_ms INTEGER NOT NULL,
    PRIMARY KEY (pr_key, principal)
);
CREATE TABLE pr_agent_links (
    link_id        TEXT PRIMARY KEY,
    pr_key         TEXT NOT NULL,
    agent_id       TEXT NOT NULL,
    agent_urn      TEXT NOT NULL,
    task           TEXT NOT NULL,
    forge_login    TEXT NOT NULL DEFAULT '',
    dedupe_key     TEXT NOT NULL UNIQUE,
    forge_assigned INTEGER NOT NULL DEFAULT 0,
    message_id     TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL,
    reminders      INTEGER NOT NULL DEFAULT 0,
    note           TEXT NOT NULL DEFAULT '',
    checks_json    TEXT NOT NULL DEFAULT '',
    assigned_by    TEXT NOT NULL DEFAULT '',
    created_ms     INTEGER NOT NULL,
    updated_ms     INTEGER NOT NULL,
    error          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX pr_agent_links_pr ON pr_agent_links(pr_key, status);
CREATE TABLE agents_controls (
 request_id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('nudge','restart')),
 session_id TEXT NOT NULL,
 state TEXT NOT NULL,
 step TEXT,
 error TEXT,
 interrupt TEXT,
 message_id TEXT,
 handoff INTEGER NOT NULL DEFAULT 0,
 new_session_id TEXT,
 discarded_pending INTEGER,
 wait_ends_at INTEGER,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
, relaunched_by TEXT, scopes_dropped TEXT);
CREATE INDEX agents_controls_session ON agents_controls(session_id, created_ms);
CREATE TABLE IF NOT EXISTS "app_agent_messages" (
 id TEXT PRIMARY KEY,
 thread_id TEXT NOT NULL REFERENCES app_agent_threads(id),
 seq INTEGER NOT NULL,
 role TEXT NOT NULL CHECK (role IN ('operator','agent')),
 body TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 runtime_turn TEXT NOT NULL DEFAULT '',
 tools TEXT NOT NULL DEFAULT '[]',
 created_ns INTEGER NOT NULL,
 updated_ns INTEGER NOT NULL,
 kind TEXT NOT NULL DEFAULT 'text' CHECK (kind IN ('text','card','plan','system','run')),
 meta_json TEXT,
 UNIQUE (thread_id, seq)
);
CREATE INDEX app_agent_messages_thread ON app_agent_messages(thread_id, seq);
CREATE INDEX app_agent_messages_state ON app_agent_messages(state) WHERE state = 'streaming';
CREATE TABLE terminal_sessions (
 id TEXT PRIMARY KEY,
 thread_ref TEXT,
 machine_id TEXT NOT NULL,
 ssh_user TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK (state IN ('open','stale','closed')),
 opened_by TEXT NOT NULL,
 opened_ns INTEGER NOT NULL,
 ended_ns INTEGER,
 bytes_in INTEGER NOT NULL DEFAULT 0,
 bytes_out INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX terminal_sessions_state ON terminal_sessions(state);
CREATE TABLE terminal_runs (
 run_id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 machine_id TEXT NOT NULL,
 ssh_user TEXT NOT NULL DEFAULT '',
 command TEXT NOT NULL,
 op_digest TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 requested_by TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('pending_approval','running','finished','failed','stopped','denied','expired')),
 approval_mode TEXT NOT NULL CHECK (approval_mode IN ('user','policy')),
 rule_id TEXT,
 decided_by TEXT,
 decided_ns INTEGER,
 exit_code INTEGER,
 output_tail TEXT NOT NULL DEFAULT '',
 output_truncated INTEGER NOT NULL DEFAULT 0,
 close_on_complete INTEGER NOT NULL DEFAULT 0,
 closed_ns INTEGER,
 stopped_by TEXT,
 error TEXT NOT NULL DEFAULT '',
 message_ref TEXT,
 supersedes TEXT,
 started_ns INTEGER,
 ended_ns INTEGER,
 duration_ms INTEGER,
 created_ns INTEGER NOT NULL
);
CREATE INDEX terminal_runs_thread ON terminal_runs(thread_ref, created_ns);
CREATE INDEX terminal_runs_state ON terminal_runs(state);
CREATE TABLE terminal_proposals (
 id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL UNIQUE REFERENCES terminal_runs(run_id),
 thread_ref TEXT NOT NULL,
 session_id TEXT,
 machine_id TEXT NOT NULL,
 command TEXT NOT NULL,
 command_digest TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 requested_by TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('pending','approved','denied','superseded','expired')),
 item_id TEXT,
 created_ns INTEGER NOT NULL,
 expires_ns INTEGER NOT NULL,
 supersedes TEXT
);
CREATE INDEX terminal_proposals_thread ON terminal_proposals(thread_ref, state);
CREATE TABLE forge_audit (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 ts_ms INTEGER NOT NULL,
 op TEXT NOT NULL,
 pr_key TEXT NOT NULL,
 actor_ref TEXT NOT NULL DEFAULT '',
 assurance TEXT NOT NULL DEFAULT 'browser-session',
 outcome TEXT NOT NULL,
 error_code TEXT,
 github_ref TEXT,
 head_sha TEXT,
 audit_class TEXT
);
CREATE INDEX forge_audit_ts ON forge_audit(ts_ms);
CREATE TABLE service_identity_agents (
 identity_id TEXT NOT NULL REFERENCES service_identities(id),
 agent_key TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 PRIMARY KEY(identity_id, agent_key)
);
CREATE TABLE service_proxy_pin (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 identity_id TEXT NOT NULL REFERENCES service_identities(id),
 pinned_at INTEGER NOT NULL
);
CREATE TABLE chat_session_lineage (
 session_id TEXT PRIMARY KEY,
 agent_id TEXT NOT NULL,
 continues_id TEXT,
 cleared INTEGER NOT NULL DEFAULT 0,
 seen_ns INTEGER NOT NULL
, continue_on INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS "bus_events" (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 tether_seq INTEGER NOT NULL,
 tether_event_id TEXT NOT NULL UNIQUE,
 at_ns INTEGER NOT NULL,
 type TEXT NOT NULL CHECK (type IN ('deploy','restart','task','failure','host','workstream','lifecycle','card','message','seat')),
 subject TEXT NOT NULL,
 actor TEXT NOT NULL DEFAULT '',
 app TEXT NOT NULL DEFAULT '',
 severity TEXT NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warn','error')),
 refs_json TEXT NOT NULL DEFAULT '[]',
 card_json TEXT,
 tether_summary TEXT NOT NULL DEFAULT '',
 cell TEXT NOT NULL DEFAULT ''
);
CREATE INDEX bus_events_seq ON bus_events(tether_seq);
CREATE INDEX bus_events_type ON bus_events(type, tether_seq);
CREATE TABLE seat_status (
 agent_urn TEXT PRIMARY KEY,
 handle TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 ref TEXT NOT NULL DEFAULT '',
 owner TEXT NOT NULL DEFAULT '',
 expires_at_ns INTEGER,
 set_by TEXT NOT NULL DEFAULT '',
 at_ns INTEGER NOT NULL,
 tether_seq INTEGER NOT NULL
);
CREATE INDEX seat_status_owner ON seat_status(owner, state);
CREATE INDEX chat_plans_thread ON chat_plans(thread_ref, position) WHERE scope='thread';
CREATE TABLE ceremony_daily (
 day TEXT PRIMARY KEY,          -- YYYY-MM-DD (UTC)
 activity INTEGER NOT NULL,     -- agent events collected in the 7 days to that day
 done INTEGER NOT NULL,         -- Torque tasks closed done in those 7 days
 gates INTEGER NOT NULL,        -- gates raised in those 7 days
 computed_ns INTEGER NOT NULL
);
CREATE TABLE checkpoint_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 correlation_id TEXT NOT NULL,
 state TEXT NOT NULL,
 at_ns INTEGER NOT NULL,
 tether_seq INTEGER NOT NULL,
 UNIQUE(correlation_id, tether_seq)
);
CREATE INDEX checkpoint_events_at ON checkpoint_events(at_ns);
