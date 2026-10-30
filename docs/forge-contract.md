# Pull requests (forge) contract

Contracts and fixture-backed stubs for the Pull requests GUI (PR0, CW-20261030-0085).
Nothing here talks to GitHub: the real backend (poll-and-index worker, guarded merge,
assign-agent, shared triage state) follows in PR1-PR4. The shapes are fixed here so the
GUI can build against them.

- Package: `internal/forgeapi` (`contract.go`, `stub.go`, `openapi.yaml`, `fixtures/`).
- Enable: `TACHYON_FORGE_STUBS=1`. Off by default; mounted route by route in `server.go`
  behind `operator.RequireOperator` (GETs open, writes need an operator session and same origin).
- Prefixes: `/api/plugins/console/v1/forge` and `/api/plugins/console/v1/console/state`.
- Spec: `internal/forgeapi/openapi.yaml` (also served at `GET .../forge/openapi.yaml`).
- SSE replay: `scripts/forge-sse-replay [base-url] [last-event-id]`.

## Conventions

- GitHub is the system of record, reached through Tachyon's one direct client. PR key: `github:owner/name#N`.
- Existing routes keep their camelCase shapes and the `{"data":...}` / `{"error":{code,message}}`
  envelope. New fields and routes are snake_case with unix-ms times.
- Repo allow-list: unknown repo 403; non-`github` host 501; malformed owner/name 400.
- Every write needs `Idempotency-Key`. Same key and body replays the first response
  (`X-Tachyon-Idempotent-Replay: 1`); same key with a different body is `idempotency_conflict`.
- Merge and review pin the head SHA (`head_sha`, 40 hex). A moved head is 409 `head_moved`
  carrying the current head. Inline comments need `commit_id` equal to the head.
- Agent callers (`Authorization` header) get 403 `requester_cannot_approve` on every write.
- Write levels (P1 decided, design addendum1): every write incl. merge is browser-session; only the operator merges from the UI; agents get no forge writes. `StepUpLevels` reserves merge -> operator-token; flip `StepUpEnabled` to swap it in.

## Stub controls

| Header | Effect |
|---|---|
| `X-Stub-Status: N` | force that status with an error envelope |
| `X-Stub-Scenario: rate_limited` | 429, `Retry-After: 42` |
| `X-Stub-Scenario: credential_missing` | 401 |
| `X-Stub-Scenario: degraded` | cached, degraded pending and status |
| `X-Stub-Scenario: mergeable_pending` | preflight state `unknown` -> `pending` |
| `X-Stub-Scenario: not_elevated` | merge refused with `operator_access_required`, only while `StepUpEnabled` |
| `X-Stub-Scenario: assign_failed` or `assign_no_login` | assign-agent resume-after-failure and no forge login |
| `X-Stub-Scenario: gap` | stream emits `resync` |

Fixture PRs: tachyon #201 (checks failed, conflicts, changes requested), #202 (ready),
#203 (already assigned; replay/reminder), #204, #205; cerberus #55 (draft); tachyon #150
(closed); agent-os #9 (snoozed, unknown mergeability) and #10 (branch protection).

## Triage state

`/console/state` is the shared item-state store with the existing UI `StateStore` shape
(`key,status,snoozeUntil,resolution,note,by,updatedAt`). Ops `read|dismiss|snooze|settle|reopen`
keep the existing `type` field; `expected_version`, `version` and `preset` (`1h|4h|tomorrow`)
are additive. Keys must carry a known prefix (`pr-inbox:`, `pr-assign:`); slashes in a key
are path-escaped (`%2F`). `POST /bulk` applies a list and reports per-op results. Snooze is
limited to 90 days.

## Stream

`GET /forge/stream` is SSE with `Last-Event-ID` / `?after=` resume; `?once=1` ends after
the script. An id outside the ring gives a `resync` event: refetch lists. Events:
`pr.updated`, `pr.merged`, `triage.changed`, `status`, `resync`.

## Reads served for real (PR4)

`internal/forgereads` serves, with the shared GitHub token: `GET /viewer`, `GET /repos/github/{o}/{n}/commits/{ref}` (adds `files`),
`.../pulls/{num}/files` (metadata, `cursor` = next GitHub page, 100 per page), `.../diff` (all files, or one with `?path=`; the file list
is kept 30 s so opening several files costs one fetch; patches over 200 KiB and patches GitHub withholds come back `truncated`, binary
files have no hunks; at most 3000 files) and `.../pulls/{num}/checks` (head sha, check runs failures first, then in-flight, then
passing, plus commit statuses). A rate limit is `429 rate_limited` with `retry_after_s`. Other hosts are `501 unavailable`.
Mergeable retry belongs to merge preflight (PR5). The stub (`TACHYON_FORGE_STUBS=1`) still owns these patterns when on.

## Review validate, atomic review and drafts (PR6, real)

`internal/forgereview`, mounted explicitly (stand down under `TACHYON_FORGE_STUBS=1`; replaces the legacy console plugin's review route). Writes
sit behind the operator route guard and the API cache; the draft read is never cached. Nothing here adds a guard, audit row or rate limit.

- `POST .../pulls/{n}/reviews/validate` `{head_sha, comments[{path,line,side,body}]}`: reads the current head and diff and answers
  `{head_sha_current, head_moved, comments[{index, status: ok|outdated|invalid_line}]}`. A comment is `ok` when its line (RIGHT = new file,
  LEFT = old file) appears in a hunk of the current diff, `outdated` when it does but the head moved, `invalid_line` otherwise.
- `POST .../pulls/{n}/reviews` `{verdict, body?, comments[], head_sha}`, `Idempotency-Key` required: refuses 409 `head_moved` (with the
  current head) unless the head matches, then sends ONE GitHub review (`commit_id` = the pinned head) and returns 201
  `{review_id, comment_ids[], state}`. `approve` needs no body; `request_changes` and `comment` need a body or comments. The key is written into
  the review body as `<!-- tachyon:idem=<key> -->`; a retry with the same key finds that review, posts nothing and returns 200 with
  `replayed: true`. GitHub's refusals (an inline comment off the diff, approving one's own PR) are 422 `invalid_args` with its message.
- `GET|PUT|DELETE .../pulls/{n}/review-draft`: one draft per pull request (`forge_review_drafts`, migration 045), `{head_sha, draft}` in,
  `{pr_key, head_sha, updated_ms, draft}` out; no draft is 404. The draft is not cleared by a submit; the page deletes it.
