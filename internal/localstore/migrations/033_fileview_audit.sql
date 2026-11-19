-- File viewer audit (V6). Additive. Metadata only: no file content, no query strings. Denials are always
-- recorded. A stat batch is one row with count = number of paths. Pruned daily by fileview.audit_retention_days.
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
