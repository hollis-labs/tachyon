-- Chat workspace items and scratchpads (T4). Additive. Rows are scoped by thread_ref;
-- file bytes live under $TACHYON_DATA_DIR/workspace/<thread-hash>/<id>, never in the database.
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
