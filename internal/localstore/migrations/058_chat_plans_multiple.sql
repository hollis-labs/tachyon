-- A thread may hold several plans (CW-20261112-0067). The one-per-thread unique index goes; plans are ordered by
-- position. Every existing plan becomes plan 1 of its thread (the default of 1).
DROP INDEX chat_plans_thread;
ALTER TABLE chat_plans ADD COLUMN position INTEGER NOT NULL DEFAULT 1;
CREATE INDEX chat_plans_thread ON chat_plans(thread_ref, position) WHERE scope='thread';
