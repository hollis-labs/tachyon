-- Builder change service (B3, CW-20261030-0036). A change is one typed edit of catalog files that becomes a
-- Tether plan. The body holds the API shape (masked diff, checks, plan id, result), never file content or
-- environment values. catalog_change_paths holds the target paths of OPEN changes: one open change per path.
-- Additive. Take the next free migration number at merge.
CREATE TABLE catalog_changes (
    change_id  TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    operator   TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL,
    request    TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_ns INTEGER NOT NULL,
    updated_ns INTEGER NOT NULL
);
CREATE INDEX catalog_changes_state ON catalog_changes(state, updated_ns);
CREATE INDEX catalog_changes_operator ON catalog_changes(operator, created_ns);
CREATE TABLE catalog_change_paths (
    path      TEXT PRIMARY KEY,
    change_id TEXT NOT NULL REFERENCES catalog_changes(change_id)
);
