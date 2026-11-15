-- The old build's migration 059, carried into the new schema (port plan section 1: a new old migration is
-- always a parity-carry). The statements are the old file's own, so the stored sqlite_master text equals a
-- database the old build migrated to 059 (schema_test.go proves it). Like 00001, this file describes the old
-- schema: do not edit it.
--
-- +goose Up
-- +goose StatementBegin
-- Ceremony meter (CW-20261114-0023f): one row per day with that day's rolling 7-day figures, so the meter can
-- compare this week with last week (local events are kept 7 days). Additive.
CREATE TABLE ceremony_daily (
 day TEXT PRIMARY KEY,          -- YYYY-MM-DD (UTC)
 activity INTEGER NOT NULL,     -- agent events collected in the 7 days to that day
 done INTEGER NOT NULL,         -- Torque tasks closed done in those 7 days
 gates INTEGER NOT NULL,        -- gates raised in those 7 days
 computed_ns INTEGER NOT NULL
);

-- Torque checkpoint transitions seen on the collector's live stream (checkpoint.state_changed): the meter reads
-- each checkpoint's type and times from Torque by correlation_id. Kept 8 days. No Tether history query is needed.
CREATE TABLE checkpoint_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 correlation_id TEXT NOT NULL,
 state TEXT NOT NULL,
 at_ns INTEGER NOT NULL,
 tether_seq INTEGER NOT NULL,
 UNIQUE(correlation_id, tether_seq)
);
CREATE INDEX checkpoint_events_at ON checkpoint_events(at_ns);
-- +goose StatementEnd
