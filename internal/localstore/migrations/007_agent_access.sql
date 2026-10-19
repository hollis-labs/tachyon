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
