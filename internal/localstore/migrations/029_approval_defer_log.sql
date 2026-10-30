-- Defer (snooze) overlay and the append-only decision log for the approvals queue.
-- The hitl record stays the source of truth: a deferral only changes ordering and batch
-- eligibility, and terminal events are reconstructible from the records (Store.BackfillLog).
CREATE TABLE approval_defers (
    item_id     TEXT PRIMARY KEY REFERENCES approval_items(id),
    until_ns    INTEGER NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    deferred_by TEXT NOT NULL DEFAULT '',
    created_ns  INTEGER NOT NULL
);

CREATE TABLE approval_decision_log (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    TEXT NOT NULL UNIQUE,
    ts_ns       INTEGER NOT NULL,
    item_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT '',
    actor_ref   TEXT NOT NULL DEFAULT '',
    assurance   TEXT NOT NULL DEFAULT '',
    decision    TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    reason_code TEXT NOT NULL DEFAULT '',
    batch_id    TEXT NOT NULL DEFAULT '',
    revision    INTEGER NOT NULL DEFAULT 0,
    extra       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX approval_decision_log_item ON approval_decision_log(item_id, seq);
CREATE INDEX approval_decision_log_kind ON approval_decision_log(kind, status, seq DESC);

CREATE TRIGGER approval_decision_log_no_update BEFORE UPDATE ON approval_decision_log
BEGIN SELECT RAISE(ABORT, 'approval_decision_log is append-only'); END;
CREATE TRIGGER approval_decision_log_no_delete BEFORE DELETE ON approval_decision_log
BEGIN SELECT RAISE(ABORT, 'approval_decision_log is append-only'); END;
