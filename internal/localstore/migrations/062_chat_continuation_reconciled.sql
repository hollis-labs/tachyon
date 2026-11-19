-- A one-pair association may have an unseen ancestor. Only verified complete
-- chains populate this local cache, atomically with all ancestry and plan moves.
-- It conveys no native admission authority.
CREATE TABLE chat_continuation_reconciled (session_id TEXT PRIMARY KEY);
