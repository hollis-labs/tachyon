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
