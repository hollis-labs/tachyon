-- The old build's migrations 057 and 058, carried into the new schema (port plan section 1: a new old
-- migration is always a parity-carry). The statements are the old files' own, in order, so the stored
-- sqlite_master text equals a database the old build migrated to 058 (schema_test.go proves it). Like
-- 00001, this file describes the old schema: do not edit it. Later old migrations get their own file.
--
-- +goose Up
-- +goose StatementBegin
-- Each seat's latest declared status (CW-20261114-0023c/d), from Tether's seat.status events (typed messages,
-- CW-20261114-0013): available, working, waiting-decision, waiting-agent, blocked, paused or offline, with an
-- optional one-line reason, ref, owner (@handle) and expiry. One row per agent; a later event replaces it. Read by
-- the needs-you view (statuses owned by @chrispian) and the seat status cards. Additive.
CREATE TABLE seat_status (
 agent_urn TEXT PRIMARY KEY,
 handle TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 ref TEXT NOT NULL DEFAULT '',
 owner TEXT NOT NULL DEFAULT '',
 expires_at_ns INTEGER,
 set_by TEXT NOT NULL DEFAULT '',
 at_ns INTEGER NOT NULL,
 tether_seq INTEGER NOT NULL
);
CREATE INDEX seat_status_owner ON seat_status(owner, state);
-- A thread may hold several plans (CW-20261112-0067). The one-per-thread unique index goes; plans are ordered by
-- position. Every existing plan becomes plan 1 of its thread (the default of 1).
DROP INDEX chat_plans_thread;
ALTER TABLE chat_plans ADD COLUMN position INTEGER NOT NULL DEFAULT 1;
CREATE INDEX chat_plans_thread ON chat_plans(thread_ref, position) WHERE scope='thread';
-- +goose StatementEnd
