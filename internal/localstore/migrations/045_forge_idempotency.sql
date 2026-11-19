-- Forge writes that must not repeat (merge first): the stored answer for an Idempotency-Key, kept 24 hours.
CREATE TABLE forge_idempotency (
    key             TEXT NOT NULL,
    op              TEXT NOT NULL,
    request_digest  TEXT NOT NULL,
    response_status INTEGER NOT NULL,
    response_json   TEXT NOT NULL,
    created_ms      INTEGER NOT NULL,
    PRIMARY KEY (key, op)
);
CREATE INDEX forge_idempotency_created ON forge_idempotency(created_ms);
