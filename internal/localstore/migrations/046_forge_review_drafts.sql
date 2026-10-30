-- A review in progress (verdict, body, queued inline comments), kept per pull request so it survives a reload or another device.
CREATE TABLE forge_review_drafts (
    pr_key     TEXT NOT NULL,
    principal  TEXT NOT NULL,
    head_sha   TEXT NOT NULL,
    body_json  TEXT NOT NULL,
    updated_ms INTEGER NOT NULL,
    PRIMARY KEY (pr_key, principal)
);
