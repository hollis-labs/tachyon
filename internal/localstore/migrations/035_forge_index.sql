-- PR3: the rebuildable index of open pull requests behind the pending inbox, and its refresh state.
CREATE TABLE forge_pr_index (
    pr_key      TEXT PRIMARY KEY,
    repo        TEXT NOT NULL,
    number      INTEGER NOT NULL,
    updated_at  TEXT NOT NULL,
    head_sha    TEXT NOT NULL DEFAULT '',
    pending     INTEGER NOT NULL DEFAULT 0,
    row_json    TEXT NOT NULL,
    fetched_ms  INTEGER NOT NULL
);
CREATE INDEX forge_pr_index_repo ON forge_pr_index(repo);

CREATE TABLE forge_index_state (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
);
