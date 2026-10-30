# Approvals defer, decision log, history and audit (A4)

Migration 029 adds `approval_defers` (the snooze overlay) and `approval_decision_log` (append-only; update
and delete are refused by triggers). The hitl record stays the source of truth.

## Defer

- `POST /items/{id}/defer {expected_revision, duration_s | until_ms, reason?}`: default 3600 s, 60..604800,
  reason at most 280 characters. A browser session is enough (it decides nothing). Clamped to the item's
  `expires_at`; the response says `expires_before_snooze_ends`. A stale revision or an already decided item
  is 409 (the usual hitl error body, with the existing outcome). `DELETE /items/{id}/defer` removes it
  (idempotent). Only `DELETE .../defer` is accepted for DELETE.
- The item stays pending and can still expire or be decided; a snooze only changes ordering (deferred
  last), adds `deferred_until` and the `deferred` batch blocker, and drops it from the batchable count.
  Deciding the item clears the overlay in the same transaction.
- Events: `item.deferred {deferred_until}` and `item.undeferred` on the A5 stream.

## Decision log

Rows: kind, status, actor ref and assurance, decision, note (clipped to 2000), reason code, batch id,
revision, extra. Written:

| Kind | When | Source |
|---|---|---|
| resolved, expired, withdrawn | in the record's own transaction on any terminal swap (browser resolve, trusted completion, sweeper, requester withdraw) | derived from the record; deterministic id `evt_<item>_terminal`, so `Store.BackfillLog` repairs gaps (runs at start) |
| deferred, undeferred | with the overlay change | log only |
| presented | on `POST /items/{id}/present`, once per session and item | log only |

Not written yet: `batch_resolved` (A2), `confirmation_failed`, `access_denied` (A3). Failed and superseded
items have no decision to show and are not logged.

## Reads (browser session)

- `GET /history?status=approved|denied|answered|expired|withdrawn&requester=&q=&from=&to=&kind=&limit=&cursor=`:
  newest first, decided kinds by default (`kind=` selects another, e.g. `deferred`). Row:
  `{event_id, item_id, kind, decision, status, actor{ref,label,assurance}, at, note, reason, batch_id?, revision, mini{title,risk,group}}`.
  `next_cursor` is opaque. `q` matches the item's title/summary/requester/id and the note.
- `GET /items/{id}/audit`: that item's events, oldest first.
- Actor label: `You` for the current session, `Operator session` for another, `System` when there is no
  actor (expiry). There is no person model; the ref is a hash of the session cookie.
- `mini` comes from the queue index and is empty for an item the index does not know.
