-- Builder Library parts (B5, CW-20261030-0038): Tachyon-managed prompts and policy bundles that are compiled into
-- agent files. A part changes only when the change that edits it is applied. Additive; take the next free number at
-- merge.
CREATE TABLE library_parts (
    kind       TEXT NOT NULL CHECK (kind IN ('prompt','policy')),
    id         TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    summary    TEXT NOT NULL DEFAULT '',
    body_json  TEXT NOT NULL,
    revision   INTEGER NOT NULL,
    archived   INTEGER NOT NULL DEFAULT 0,
    created_ns INTEGER NOT NULL,
    updated_ns INTEGER NOT NULL,
    PRIMARY KEY (kind, id)
);
CREATE TABLE library_part_revisions (
    kind      TEXT NOT NULL,
    id        TEXT NOT NULL,
    revision  INTEGER NOT NULL,
    body_json TEXT NOT NULL,
    actor     TEXT NOT NULL DEFAULT '',
    plan_id   TEXT NOT NULL DEFAULT '',
    at_ns     INTEGER NOT NULL,
    PRIMARY KEY (kind, id, revision)
);
CREATE TABLE part_refs (
    agent_id      TEXT NOT NULL,
    kind          TEXT NOT NULL,
    part_id       TEXT NOT NULL,
    part_revision INTEGER NOT NULL,
    compiled_hash TEXT NOT NULL,
    plan_id       TEXT NOT NULL DEFAULT '',
    applied_ns    INTEGER NOT NULL,
    PRIMARY KEY (agent_id, kind, part_id)
);
CREATE INDEX part_refs_part ON part_refs(kind, part_id);
