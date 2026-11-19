-- Agents derivation (A2). Additive. Derived fields only: a one-liner, context figures, token
-- counts and per-minute samples. Nothing here can hold raw model output or tool arguments.
ALTER TABLE agents_sessions ADD COLUMN one_liner_json TEXT;
ALTER TABLE agents_sessions ADD COLUMN calls_this_turn INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents_sessions ADD COLUMN ctx_used INTEGER;
ALTER TABLE agents_sessions ADD COLUMN ctx_window INTEGER;
ALTER TABLE agents_sessions ADD COLUMN ctx_model TEXT;
ALTER TABLE agents_sessions ADD COLUMN compactions_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agents_sessions ADD COLUMN last_clear_ns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents_sessions ADD COLUMN last_clear_inferred INTEGER NOT NULL DEFAULT 0;
-- NULL tokens = not metered (never shown as 0).
ALTER TABLE agents_sessions ADD COLUMN tok_in INTEGER;
ALTER TABLE agents_sessions ADD COLUMN tok_out INTEGER;
ALTER TABLE agents_sessions ADD COLUMN tok_cr INTEGER;
ALTER TABLE agents_sessions ADD COLUMN tok_cw INTEGER;
CREATE TABLE agents_samples (
 session_id TEXT NOT NULL,
 bucket_ns INTEGER NOT NULL,
 bucket_kind TEXT NOT NULL CHECK (bucket_kind IN ('min','hour')),
 activity INTEGER NOT NULL DEFAULT 0,
 ctx_used INTEGER,
 ctx_window INTEGER,
 tok_in INTEGER NOT NULL DEFAULT 0,
 tok_out INTEGER NOT NULL DEFAULT 0,
 tok_cr INTEGER NOT NULL DEFAULT 0,
 tok_cw INTEGER NOT NULL DEFAULT 0,
 tool_calls INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (session_id, bucket_kind, bucket_ns)
);
