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

## Merge preflight and merge (PR5, real)

`internal/forgemerge`, mounted explicitly (stand down under `TACHYON_FORGE_STUBS=1`). Preflight is open like the other forge reads; the
merge sits behind the operator route guard (browser session) and the API cache, so a merge invalidates the forge reads. Step-up stays
reserved in `forgeapi.StepUpLevels`; nothing here adds a guard, audit row or rate limit.

- `GET .../pulls/{n}/merge-preflight`: `state` ready|blocked|pending. Blockers from GitHub's own facts: `closed`, `draft`, `checks_failing`,
  `checks_pending` (check runs and statuses at the head), `changes_requested` (each reviewer's latest verdict), `conflicts`, `behind_base`
  (`behind_by` from a compare), `review_required` (only when the branch protection is readable) and the generic `branch_protection` when
  GitHub says blocked and nothing explains it. Unknown mergeability is re-read twice (1.5 s apart), then `pending`, never a guess.
  `methods_allowed` follows the repository settings, default squash. `required_checks` counts all checks at the head: GitHub does not say
  which are required to a token without admin rights.
- `PUT .../pulls/{n}/merge` `{method, head_sha, commit_title?, commit_message?, delete_branch?}`, `Idempotency-Key` required (1 to 128).
  It re-runs preflight: a moved head is 409 `head_moved` (with the current `head_sha`), blockers are 409 `merge_blocked`. The head sha is
  passed to GitHub as `sha`, so GitHub still enforces the repository's rules. A refusal from GitHub is 409 `conflict` with its message.
  If the outcome is unknown (outage, odd answer, 405) the PR is read again and a merged PR is reported as merged. Success refreshes the
  PR index (which publishes `pr.merged`), settles the `pr-inbox:` triage entry and returns `{merged, sha, message, method, audit_class,
  settled, links_done}`. `delete_branch` is a best-effort delete after the merge, only for a branch in the same repository.
- Idempotency: the answer for a key is stored 24 hours (`forge_idempotency`, migration 044); the same key and body returns it, the same
  key with a different body is 409 `idempotency_conflict`. Only a merge or a `head_moved` answer is stored, so a refusal can be retried.
- `audit_class: "lifecycle"` is the contract's label only. No audit row is written (deferred to CW-20261030-0082). `links_done` counts the agent links closed by
  the merge (PR7).

## Assign agent (PR7, real)

`POST /forge/repos/{host}/{owner}/{name}/pulls/{num}/assign-agent` (explicit mount, behind the operator route guard,
stands down under `TACHYON_FORGE_STUBS=1`). Body `{agent_id, task, note?, checks?, force?}`; unknown fields are refused.
Answer `{data:{link, replayed, reminded?}}`.

- The pull request must be open on GitHub (`409 pr_not_open`). The agent comes from Tether's catalog
  (`404 agent_not_found`; `422 agent_not_addressable` when it has no URN).
- One link row per `pr-assign:<pr_key>:<agent>:<task>` (table `pr_agent_links`). Steps are recorded on it:
  `pending` -> `assigned` (GitHub assignee tried) -> `sent` (Tether message accepted). A failed send leaves status
  `failed` with `error`; repeating the call does only the message. A call on a `sent` link is a `replayed` no-op;
  `force:true` sends a reminder (`reminders` + 1, `reminded:true`).
- GitHub login: app setting `forge.agent_logins` (`{agent_id: login}`), else the agent id if that GitHub user exists,
  else none (`forge_assigned:false`, not an error).
- The message is a `notice` to the agent URN, subject `[PR] <Task label>: owner/repo#n`, readable text and a fenced
  `console-pr-assignment` block (`console.pr_assignment/v1`; `checks_failing` for `fix_checks`). It is sent with the
  token in `TACHYON_TETHER_MESSAGE_TOKEN_FILE` (else `TETHER_TOKEN_FILE`) which needs the Tether `messages` scope.
  The Tether envelope has no idempotency field, so repeat protection is the link row, not delivery.
- Open links appear as `kind:"agent"` entries in a pending row's `assignees` (`urn`, `task`, `link_status`), and
  `pr.updated{changed:["assignees"]}` is published. A merge through PR5 sets the PR's open links to `done`
  (`links_done`). Agents are told not to merge; that is guidance, not a technical block.

## Status and curl matrix (PR9)

What is real on the live host (the stub, `TACHYON_FORGE_STUBS=1`, must stay off there). `B` = `https://<host>/api/plugins/console/v1/forge`,
`P` = `repos/github/<owner>/<repo>/pulls/<n>`. Writes need an operator browser session (cookie plus same-origin `Origin`); a bearer token is
refused. `HEAD` is the 40-hex head sha from a read.

| Area | Route | State |
|---|---|---|
| Pending inbox, status, stream | `GET B/pulls/pending`, `GET B/status`, `GET B/stream` | real (PR3) |
| Triage | `PUT /console/state/pr-inbox:<prKey>` | real |
| Viewer, commit, files, diff, checks | `GET B/viewer`, `B/repos/github/<o>/<r>/commits/<ref>`, `B/<P>/files`, `/diff`, `/checks` | real (PR4) |
| Merge preflight, merge | `GET B/<P>/merge-preflight`, `PUT B/<P>/merge` | real (PR5) |
| Review validate, review, drafts | `POST B/<P>/reviews/validate`, `POST B/<P>/reviews`, `GET/PUT/DELETE B/<P>/review-draft` | real (PR6) |
| Assign agent | `POST B/<P>/assign-agent` | real (PR7) |
| Audit, step-up, rate limits | none | deferred (CW-20261030-0082) |

```sh
curl -s "$B/pulls/pending?state=active&limit=20"                       # inbox
curl -s "$B/$P/merge-preflight"                                       # blockers / ready
curl -s -b jar -H "Origin: $ORIGIN" -H "Idempotency-Key: $(uuidgen)" -X PUT \
  -d '{"method":"squash","head_sha":"'$HEAD'"}' "$B/$P/merge"          # 409 head_moved / merge_blocked
curl -s -b jar -H "Origin: $ORIGIN" -X POST \
  -d '{"agent_id":"builder","task":"fix_checks"}' "$B/$P/assign-agent" # {link, replayed}
```

Refusals use the shared envelope `{error:{code,message}}`; the codes are listed in Conventions and per section above.
