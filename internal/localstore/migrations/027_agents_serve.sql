-- Agents sessions API and stream (A3). Additive.
-- Agent identity and lineage as Tether reported them (empty = Tether did not say).
ALTER TABLE agents_sessions ADD COLUMN logical_agent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agents_sessions ADD COLUMN agent_name TEXT NOT NULL DEFAULT '';
ALTER TABLE agents_sessions ADD COLUMN parent_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agents_sessions ADD COLUMN replaced_by TEXT NOT NULL DEFAULT '';
-- The replayable part of GET agents/stream: bus events, control state changes and gaps share one
-- id sequence so Last-Event-ID is a single integer. data_json holds the same derived, bounded
-- shape the matching REST route returns; never raw output.
CREATE TABLE agents_stream (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK (type IN ('bus','control','gap')),
 at_ns INTEGER NOT NULL,
 data_json TEXT NOT NULL
);
CREATE INDEX agents_stream_at ON agents_stream(at_ns);
