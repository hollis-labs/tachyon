# Approvals assurance and the event stream (A5)

## assurance.Provider

`internal/approvals/assurance` is the one place session strength is asked. Code that gates an action calls
`Provider.Require(r, action)` or `assurance.Required(action)`; it does not read the Tangent bridge.
The only implementation is `approvals.TangentProvider`, which wraps the bridge. A later login-validated
confirmation replaces that implementation (and this table) without touching callers. See CW-20261030-0082.

Levels: `none` < `browser-session` < `operator-token` (the bridge's existing spellings).

| Action | Needs |
|---|---|
| read, present, defer, resolve_low | browser-session |
| resolve_medium, resolve_high, resolve_destructive, batch_resolve, secret_submit, action_run_read | operator-token |
| anything not in the table | operator-token (fails closed) |

`resolve_high` gates stored-high and cerberus-source items (A3a, below). Response changes: the medium check
reads the table (same result), and `/elevate` and `/de-elevate` pass through the provider to the same bridge routes.

## Deciding high and cerberus items in the browser (A3a)

Temporary by design: the operator confirmed this lower posture (CW-20261030-0129); the hardening revisit is
CW-20261030-0082. Before this, such items returned `403 provider_handoff_required`.

- One gate, `approvals.CanResolve(record, now)`, feeds both the projection and the resolve handler. The projection
  carries `approvable` and, when false, `approvable_reason` (`already_resolved`, `expired`, `unsupported_class`).
  `deferred` and `decision_pending` are in the contract but not produced: a deferred item can still be decided and
  decisions are not held on the server.
- A high or cerberus item that is approvable carries `confirm: {phrase}`. The phrase is `APPROVE ` plus 8 hex
  digits derived from the item id and its `op_digest` (a hash of the stored request when it has none), so the asker
  cannot choose it and a registered action run's phrase covers the same operation that Start re-verifies.
- Approving needs `operator-token` assurance (`resolve_high`, 403 `assurance_insufficient`) and
  `response.confirmation` equal to the phrase (trimmed, case-sensitive; 422 `confirmation_required`, audited as
  `confirmation_failed`). Denying needs neither the phrase nor more than the usual check.
- Stale revisions (409 `stale_revision`) and a second submit (409) behave as for any item.

## GET /access

Normalized, no provider shapes: `{state: none|elevated, expires_in_s, can_elevate, ttl_policy,
required_for: {batch, medium, high, destructive, secrets}}`. `required_for` is derived from the table.
Needs a browser session (401 `session_required` without one).

## GET /events (SSE)

Needs a browser session. Events carry ids and state only, never item content:
`item.created|updated|resolved|expired` with `{item_id, revision, state, at}`. `item.deferred|undeferred`
arrive with the defer slice through the same broker (`approvals.Changes(db).Publish`).

- A new connection (no id) gets one `inbox.changed {revision}` baseline whose id is the current head.
- `Last-Event-ID` or `?after=` replays what was missed. The ring holds 512 changes. An id from before a
  restart (ids are epoch-based: start ms * 1000 + n), older than the ring, or past the head gets one
  `resync` event: refetch `/inbox`.
- A client too slow to keep up (64 buffered) is dropped; it reconnects with `Last-Event-ID`.
- Keepalive comment every 15 s. `?once=1` ends after the replay (scripts). Polling `/inbox` stays the fallback.
- The ring is per process. Changes made by another process are not seen (same as the hitl `Await`).
- Behavioural note (G7): expiry is still lazy until the sweeper slice (A1), so `item.expired` appears when an
  item is materialized, not at its deadline.

Changes are published after commit from `Store.Create`, `Store.Swap` and the trusted completion path.
`scripts/approvals-sse-replay` (from A0) replays the stream.

## Destructive requests, affected items and alternatives (A3b)

Card requests (`card.approval`, through `card.ask`) may carry three members, validated and cleaned in
`cards.Prepare` and covered by the `op_digest` (`cards.DigestOf`; a request without them digests exactly as before):

- `affected_items: [{ref, label, kind, note?}]`, 1 to 200. The projection adds `affected_total`.
- `destructive: {irreversible, summary}`. The server raises it when any effect op is `delete`, `purge`, `drop` or
  `force_*` (and forces `irreversible: true`); the asker may raise it but never clear it. A destructive request is at
  least class medium.
- `alternatives: [{id, label, effects[]}]`, 0 to 3 (for example "Archive instead").

Deciding a destructive item needs `operator-token` assurance for any decision (`resolve_destructive`). Approving it
needs `response.confirmation` equal to the server phrase `DELETE <n> ITEMS` (`n` = affected total; the A3a
`APPROVE ...` phrase when there are no affected items), or `response.alternative_id` naming one of its
alternatives (approval only; otherwise 400). `cardfx` then runs the chosen alternative's effects instead of the main
set, after re-verifying the digest, and records `alternative_id` on the receipt. Plain (non-card) approvals keep
their existing behaviour.
