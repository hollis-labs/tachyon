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
