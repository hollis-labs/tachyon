-- One row per (pull request, agent, task) assignment: the steps taken (GitHub assignee, Tether message) so a
-- retry does only the missing step, and the link the pending list shows as the agent's assignment.
CREATE TABLE pr_agent_links (
    link_id        TEXT PRIMARY KEY,
    pr_key         TEXT NOT NULL,
    agent_id       TEXT NOT NULL,
    agent_urn      TEXT NOT NULL,
    task           TEXT NOT NULL,
    forge_login    TEXT NOT NULL DEFAULT '',
    dedupe_key     TEXT NOT NULL UNIQUE,
    forge_assigned INTEGER NOT NULL DEFAULT 0,
    message_id     TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL,
    reminders      INTEGER NOT NULL DEFAULT 0,
    note           TEXT NOT NULL DEFAULT '',
    checks_json    TEXT NOT NULL DEFAULT '',
    assigned_by    TEXT NOT NULL DEFAULT '',
    created_ms     INTEGER NOT NULL,
    updated_ms     INTEGER NOT NULL,
    error          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX pr_agent_links_pr ON pr_agent_links(pr_key, status);
