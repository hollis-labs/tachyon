# Approvals backend contract

Slice A0 of the Approvals backend. Design: tesseract `design_tachyon_approvals_backend_20261030` (+ addendum1).
Contract: `internal/approvalsapi/openapi.yaml` (also served by the stub at `<prefix>/openapi.yaml`).
Fixtures: `internal/approvalsapi/fixtures/*.json`, embedded. Nothing here changes the real queue yet.

## Running the stub

`TACHYON_APPROVALS_STUBS=1 tachyon ...` mounts the new and extended routes on the existing prefix
`/api/plugins/console/v1/approval-inbox`, route by route. `session`, `status`, `elevate` and `de-elevate`
stay with the real bridge. Every stub response carries `X-Tachyon-Approvals-Stub: 1`.

Guards match the real routes: GETs are open, every write needs an operator session (same-origin, cookie).
Elevated access is simulated: it is on by default; send `X-Stub-Scenario: not_elevated` to see the
`operator_access_required` paths (batch, secrets, medium/destructive resolve) and `/access` state `none`.

Stub controls (request headers; the stub only):

| Header | Effect |
|---|---|
| `X-Stub-Status: 401\|403\|404\|409\|422\|429\|503` | forces that error with the `{code,message}` shape |
| `X-Stub-Scenario: not_elevated` | no operator-token assurance |
| `X-Stub-Scenario: secrets_unavailable` | `POST /secrets` answers 503 |
| `X-Stub-Scenario: gap` | `/events` answers one `resync` |
| `X-Stub-Fail-Item: <id>` | that item's result in a batch is `error` |

## Routes (relative to the prefix)

| Route | Notes |
|---|---|
| `GET /inbox` | `pending[]`, `history[]` with the projection; filters `status group risk batchable q order cursor limit`; `order=urgency` default |
| `GET /items/{id}`, `POST /items/{id}/present` | one item with the projection |
| `GET /items/{id}/audit` | the item's decision events |
| `POST /items/{id}/resolve` | adds `response.confirmation`, `response.alternative_id`; error codes below |
| `POST /items/{id}/defer`, `DELETE /items/{id}/defer` | snooze overlay; never extends expiry (`expires_before_snooze_ends`) |
| `POST /batch/resolve` | up to 25 items, in order, not a transaction; whole batch needs elevated access |
| `GET /history` | decision timeline with reasons; filters `status requester q from to limit cursor` |
| `GET /events` | SSE; replay with `Last-Event-ID` or `?after=`; `?once=1` ends after the replay (stub) |
| `GET /access` | `{state: none\|elevated, expires_in_s, can_elevate, ttl_policy, required_for}` |
| `POST /secrets` | returns a `secret://tachyon/<id>` reference, never the value |

## Decisions the fixtures carry (addendum 1)

- **G1 destructive** is a flag on a medium-risk item, not class high. It needs elevated access and, to
  approve, `response.confirmation` equal to `destructive.phrase` (case-sensitive, trimmed) else 422
  `confirmation_required`. `alternatives[]` ("Archive instead") resolve as approved with `alternative_id`
  and need no phrase. A true **high** item (`ap_destructive_high`, also carries a phrase) is
  `provider_handoff: true` and read-only here: resolve answers 403 `provider_handoff_required`.
- **G2** a batch needs elevated access even for low items. **G3** whitelist `create update restart`,
  no declared effects means not batchable, max 25.
- **SSE is primary**; polling `/inbox` every 10 s stays as the fallback. Event types: `inbox.changed`,
  `item.created|updated|resolved|expired|deferred|undeferred`, `resync`. The scripted stream has 8 events.

## Batch eligibility (the shared table)

`approvalsapi.Eligibility(Subject)` is the server rule. `fixtures/eligibility_table.json` holds 24 cases and
the canonical blocker order; the UI's pure `BatchEligibility` function should be tested against the same
table, and the Go test fails if the table and the function drift. Blockers, in order: `not_pending`,
`kind_not_batchable`, `risk_not_low`, `interactive`, `has_secret`, `command_exec`, `destructive`,
`no_effects_declared`, `irreversible_effect`, `expired`, `deferred`, `needs_operator_access`.
`kind_not_batchable` and `not_pending` are additions to the design's list. The server is authoritative.

## Fixture items

Pending: command (`ap_cmd_deploy`), batchable changes (`ap_change_a|b|c`; `b` is at revision 4 for stale,
`c` expires in 90 s), no effects (`ap_change_noeffects`), irreversible archive (`ap_change_archive`),
destructive medium with 3 affected items and an Archive alternative (`ap_destructive_med`, phrase
`DELETE 3 ITEMS`), destructive high (`ap_destructive_high`), question, form, secret field, notice, deferred.
History: approved, denied with reason, expired, withdrawn, answered, batch-resolved.

## Error codes

`stale_revision` 409 (carries `revision`), `already_resolved` 409 (carries `terminal_outcome`), `batch_id_reuse` 409,
`confirmation_required` 422, `reason_required` 422, `invalid_secret_ref` 422, `operator_access_required` 403,
`provider_handoff_required` 403, `requester_cannot_approve` 403, `session_required` 401,
`secrets_unavailable` 503, `rate_limited` 429, `invalid` 400, `not_found` 404.

## Known limits

Item `request_snapshot` is the existing shape trimmed to what the fixtures need. `run_state` is a state only
(command and output stay operator-token only). Actor labels are "You" / "Operator session" (no person model).
Secret redemption is Tachyon-run effects only (G4 open). Search covers title, summary, requester and id, not bodies.
