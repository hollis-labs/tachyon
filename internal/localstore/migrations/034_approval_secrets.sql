-- Secrets submitted from the approvals secure dialog (A6). The value is AES-256-GCM encrypted with the
-- key from TACHYON_SECRET_KEY_FILE; only a reference, size and fingerprint ever leave the table through
-- the HTTP API. A row is bound to one item and field.
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
