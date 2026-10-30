-- Builder catalog index (B1). Additive. A rebuildable read model of the Tether catalog files plus
-- masked snapshots; Tether stays the system of record. Env values never enter these tables.
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
INSERT INTO catalog_state(id) VALUES (1);
