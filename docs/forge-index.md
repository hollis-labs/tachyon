# Pull request index (PR3)

A poller keeps a rebuildable index of open pull requests and serves the pending inbox, the status and the
event stream from it. GitHub stays the system of record. Contract: `docs/forge-contract.md` (PR0).
Design: `design_tachyon_pull_requests_backend_20261030`, sections 3 and 6.

## Routes (read-only, under `/api/plugins/console/v1/forge`)

- `GET /pulls/pending?reason=&repo=&state=active|snoozed|dismissed|resolved&q=&limit=&cursor=`: rows from the
  index joined with triage state (`pr-inbox:<pr_key>` in `/console/state`: unread/read are `active`, settled is
  `resolved`). `{data: {items, next_cursor, total, cached, fetched_ms, degraded}}`; cursor `c<offset>`; unknown
  parameters are 400. Until the first refresh has completed the route falls through to the legacy handler, so the
  page is never empty at start-up.
- `GET /status`: token owner, scopes (with a warning for the classic `repo` scope), rate remaining and reset,
  watched set, last refresh, `degraded` and a note. Source of the "Cached" label.
- `GET /stream`: SSE with `pr.updated {pr_key, changed[]}`, `pr.merged`, `status {rate, degraded}` and `resync`.
  A fresh connection gets one `status`; `Last-Event-ID` or `?after=` replays, or answers one `resync` when the id
  is from before a restart (ids are `startMs*1000+n`), past the head, or older than the ring (512). `?once=1`
  ends after the replay.

## Poller

Every `forge.poll_s` (60 s) it searches open PRs for each watched entry (`owner/name` or `org:<name>`, default
`org:<GITHUB_ORG>`), up to `forge.max_pending` (100), plus `mentions:<viewer>`. Only PRs whose `updated_at` changed,
or whose checks were still pending, are hydrated (PR, reviews, check runs). GET responses are cached by ETag, so
unchanged lists answer 304. Under `forge.rate_floor` (200) remaining it stops hydrating and reports `degraded`;
on a rate limit or a missing token it keeps serving the saved rows and backs off. A PR that left the open list is
looked up (10 a cycle) and announced as merged.

Reasons: `checks_failed`, `changes_requested` (latest review per reviewer), `conflicting`, `review_requested`
(the token owner is a requested reviewer), `mentioned`, `unassigned`, `stale` (`forge.stale_days`, 7).

## Alert feed

A reason that appears on a PR that was already indexed becomes a `pr.*` event (`pr.review_requested`,
`pr.checks_failed`, `pr.changes_requested`, `pr.conflicting`, `pr.stale`, `pr.merged`) with a stable id
`pr:<pr_key>:<kind>:<head sha or updated_at>`, handed to the Alerts store as source `forge`. The first sync after an
empty index is silent.

## Settings and switches

`forge.watched`, `forge.poll_s`, `forge.stale_days`, `forge.rate_floor`, `forge.max_pending` are read from
`app_settings` behind `forgeindex.SettingsSource`; the settings registry (ST1/ST2) takes them over by replacing
that source. `TACHYON_FORGE_INDEX=0` turns the index off; `TACHYON_FORGE_STUBS=1` replaces it with the stub.
Migration `035_forge_index.sql`.

## Not here

Agent assignees from the link table (PR7), checks/files/diff reads (PR4), merge and review writes (PR5/PR6),
webhooks (not in v1; freshness is about one poll).
