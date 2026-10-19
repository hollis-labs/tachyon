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
