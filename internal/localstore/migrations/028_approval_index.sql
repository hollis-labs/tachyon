-- A derived, rebuildable index over approval_items for the browser queue: filters, search and
-- ordering. The hitl record stays the source of truth; every row can be recomputed from it
-- (approvals.Store.Reindex does that at start and after any gap). deferred_until_ns is the
-- one column written elsewhere (the defer slice) and is never rebuilt from the record.
CREATE TABLE approval_item_index (
    item_id           TEXT PRIMARY KEY REFERENCES approval_items(id),
    revision          INTEGER NOT NULL,
    state             TEXT NOT NULL,
    terminal          INTEGER NOT NULL,
    status            TEXT NOT NULL,
    kind              TEXT NOT NULL,
    grp               TEXT NOT NULL,
    risk              TEXT NOT NULL,
    risk_rank         INTEGER NOT NULL,
    title             TEXT NOT NULL,
    summary           TEXT NOT NULL,
    requester_id      TEXT NOT NULL,
    requester_label   TEXT NOT NULL,
    requester_kind    TEXT NOT NULL,
    session_ref       TEXT NOT NULL,
    created_ns        INTEGER NOT NULL,
    expires_ns        INTEGER,
    decided_ns        INTEGER,
    deferred_until_ns INTEGER,
    interactive       INTEGER NOT NULL,
    has_secret        INTEGER NOT NULL,
    command_exec      INTEGER NOT NULL,
    destructive       INTEGER NOT NULL,
    effect_ops        TEXT NOT NULL,
    search            TEXT NOT NULL
);
CREATE INDEX approval_item_index_pending ON approval_item_index(terminal, grp, risk_rank);
CREATE INDEX approval_item_index_history ON approval_item_index(terminal, status, created_ns DESC, item_id DESC);
CREATE INDEX approval_item_index_requester ON approval_item_index(requester_id);
