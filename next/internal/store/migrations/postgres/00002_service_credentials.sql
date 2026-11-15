-- Machine credentials on the identity module (slice B3), on Postgres. See
-- migrations/sqlite/00002_service_credentials.sql for what each column means.
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE service_credentials (
 credential_id   TEXT PRIMARY KEY,
 identity_id     TEXT NOT NULL REFERENCES service_identities(id),
 verbs           TEXT NOT NULL CHECK(jsonb_typeof(verbs::jsonb) = 'array'),
 kinds           TEXT NOT NULL CHECK(jsonb_typeof(kinds::jsonb) = 'array'),
 source_token_id TEXT UNIQUE REFERENCES service_tokens(id),
 created_at      BIGINT NOT NULL,
 expires_at      BIGINT NOT NULL,
 revoked_at      BIGINT,
 CHECK(expires_at > created_at)
);
CREATE INDEX service_credentials_identity ON service_credentials(identity_id);
-- +goose StatementEnd
