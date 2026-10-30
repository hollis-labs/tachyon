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

`resolve_high` is listed for the typed-phrase resolve (addendum 2) that a later slice enables; today the
inbox still refuses class high with `provider_handoff_required`. This slice changes no existing response:
the medium check now reads the table (same result), and `/elevate` and `/de-elevate` pass through the
provider to the same bridge routes.

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
