-- The old build's migrations 057 and 058, on Postgres (see migrations/sqlite/00003_old_057_058.sql).
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE seat_status (
 agent_urn TEXT PRIMARY KEY,
 handle TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 ref TEXT NOT NULL DEFAULT '',
 owner TEXT NOT NULL DEFAULT '',
 expires_at_ns BIGINT,
 set_by TEXT NOT NULL DEFAULT '',
 at_ns BIGINT NOT NULL,
 tether_seq BIGINT NOT NULL
);
CREATE INDEX seat_status_owner ON seat_status(owner, state);
DROP INDEX chat_plans_thread;
ALTER TABLE chat_plans ADD COLUMN position BIGINT NOT NULL DEFAULT 1;
CREATE INDEX chat_plans_thread ON chat_plans(thread_ref, position) WHERE scope='thread';
-- +goose StatementEnd
