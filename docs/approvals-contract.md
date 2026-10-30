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
| `X-Stub-Scenario: not_elevated`, `item_not_pending`, `secret_field_unknown`, `rate_limited` | `POST /secrets` answers the matching error; `secret_ref_invalid` makes resolve answer 422 |
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
| `POST /secrets` | JSON `{item_id, field, value<=16 KiB}` or multipart (`file<=64 KiB`); 201 `{secret_ref, created_at, expires_at, size, fingerprint}`, never the value. `Idempotency-Key` replays. Errors: 400, 403 `assurance_insufficient` (+`step_up`), 404, 409 `item_not_pending` / `secret_field_unknown` / `idempotency_key_reuse`, 413, 429, 503. Resolve takes `answers[field] = {secret_ref}`, else 422 `secret_ref_invalid` |

## Decisions the fixtures carry (addendum 1)

- **G1 destructive** is a flag on an item (medium or high), not a class of its own. It needs elevated access.
- **Typed phrase (addendum 2):** every high-class item and every destructive one carries `confirm.phrase`.
  Approving needs `response.confirmation` equal to it (case-sensitive, trimmed) else 422
  `confirmation_required`. `alternatives[]` ("Archive instead") resolve as approved with `alternative_id`
  and need no phrase. High-class items, including cerberus-source ones and high action runs, resolve in the
  browser with the phrase and a High chip. There is no provider-handoff state or error code.
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
`DELETE 3 ITEMS`), destructive high (`ap_destructive_high`), high action run (`ap_high_action`, phrase `RUN DEPLOY PRODUCTION`), high cerberus-source (`ap_high_cerberus`, phrase `RESTART GATEWAY`), question, form, secret field, notice, deferred.
History: approved, denied with reason, expired, withdrawn, answered, batch-resolved.

## Error codes

`stale_revision` 409 (carries `revision`), `already_resolved` 409 (carries `terminal_outcome`), `batch_id_reuse` 409,
`confirmation_required` 422, `reason_required` 422, `secret_ref_invalid` 422, `operator_access_required` 403,
`requester_cannot_approve` 403, `session_required` 401,
`secrets_unavailable` 503, `rate_limited` 429, `invalid` 400, `not_found` 404.

## Known limits

Item `request_snapshot` is the existing shape trimmed to what the fixtures need. `run_state` is a state only
(command and output stay operator-token only). Actor labels are "You" / "Operator session" (no person model).
Secret redemption is Tachyon-run effects only (G4 open). Search covers title, summary, requester and id, not bodies.
