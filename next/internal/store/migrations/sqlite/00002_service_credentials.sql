-- Machine credentials on the identity module (port plan section 4, slice B3).
--
-- Each identity API key that acts for a service identity has one row here: the key's id (the
-- identity module's k_... id), the service identity it acts for, its grant (the verbs and the
-- object kinds it may touch, exactly the old token's), and its expiry. A key with no row, or a
-- revoked or expired row, is refused on the agent API whatever its scopes. source_token_id names
-- the old service token it replaced (`tachyon access reissue`); that old token is superseded.
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE service_credentials (
 credential_id   TEXT PRIMARY KEY,
 identity_id     TEXT NOT NULL REFERENCES service_identities(id),
 verbs           TEXT NOT NULL CHECK(json_valid(verbs) AND json_type(verbs) = 'array'),
 kinds           TEXT NOT NULL CHECK(json_valid(kinds) AND json_type(kinds) = 'array'),
 source_token_id TEXT UNIQUE REFERENCES service_tokens(id),
 created_at      INTEGER NOT NULL,
 expires_at      INTEGER NOT NULL,
 revoked_at      INTEGER,
 CHECK(expires_at > created_at)
);
CREATE INDEX service_credentials_identity ON service_credentials(identity_id);
-- +goose StatementEnd
