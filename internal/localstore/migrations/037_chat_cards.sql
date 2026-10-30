-- Chat cards (T5a). Additive. Binds an Approvals queue item to a chat thread so the card renders in the
-- thread stream and "need you" can be derived. The queue item stays the only record of the decision.
CREATE TABLE chat_card_refs (
 item_id TEXT PRIMARY KEY,
 thread_ref TEXT NOT NULL,
 message_ref TEXT,
 kind TEXT NOT NULL,
 created_ns INTEGER NOT NULL
);
CREATE INDEX chat_card_refs_thread ON chat_card_refs(thread_ref, created_ns);
