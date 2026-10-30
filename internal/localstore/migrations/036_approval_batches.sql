-- Batch approve (CW-20261030-0133). One row per batch_id so a retried POST /batch/resolve replays the stored
-- answer instead of deciding twice. A different request under the same batch_id is refused. Rows older than a
-- week are pruned when a new batch is stored. Additive.
CREATE TABLE approval_batches (
    batch_id       TEXT PRIMARY KEY,
    request_digest TEXT NOT NULL,
    response       TEXT NOT NULL,
    created_ms     INTEGER NOT NULL
);
CREATE INDEX approval_batches_created ON approval_batches(created_ms);
