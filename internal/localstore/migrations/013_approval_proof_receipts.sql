-- Receipt and immutable HITL outcome are written in the same transaction.
-- This is decision delivery deduplication, not a provider execution ledger.
CREATE TABLE approval_proof_receipts (
 authority TEXT NOT NULL,
 proof_id TEXT NOT NULL,
 completion_id TEXT NOT NULL,
 item_id TEXT NOT NULL UNIQUE REFERENCES approval_items(id),
 expected_revision INTEGER NOT NULL,
 completion_digest TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 PRIMARY KEY(authority, proof_id),
 UNIQUE(authority, completion_id)
);
