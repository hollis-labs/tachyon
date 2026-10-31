-- Forge write audit: who did what to which PR. No bodies, no diffs; refs and outcomes only.
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
