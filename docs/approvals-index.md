# Approvals queue index, projection and expiry sweeper (A1)

## Index

`approval_item_index` (migration 028) is derived from `approval_items` and rebuildable. The hitl record
stays the source of truth. A row is written in the same transaction as the record (`Store.Create`,
`swapRecord`, so the trusted completion path is covered too). An index write error never fails a decision;
`Store.Reindex` adds or refreshes any row that is missing or behind its record, and runs at start (and is
safe to run any time). `deferred_until_ns` is the one column not derived from the record; the defer slice
(A4) writes it.

## Projection (fields added to each item, top level, additive)

`kind title summary requester{id,label,kind} session_ref asked_at risk risk_reasons group batchable
batch_blockers effect_classes expires_at server_now_ms reason_required` and `recommendation` when the
producer set one. Derivation, from the stored request:

- **risk**: the request's `class` (low|medium|high); unreadable means medium.
- **group**: `notice` (attention, card.notice), `info` (card.info), `secret` (a secret form field), `form`,
  `question`, `destructive` (any effect op delete/purge/drop/force_*), `command` (an `argv` or `action`),
  else `change`.
- **risk_reasons**: `effects_present`, `secret_field`, `interactive`, `destructive`, `command_exec`.
- **batchable / batch_blockers**: `approvalsapi.Eligibility` with the item's own facts. They describe the
  item, not the caller: whether the session may batch at all is `GET /access` (`required_for.batch`), so
  `needs_operator_access` never appears here.
- Fields owned by later slices are absent, not faked: `destructive`, `confirm`, `alternatives`,
  `affected_items` (A3), `evidence`, `run_state` (A7), `deferred_until` (A4).

## GET /inbox

Query: `status=pending|history` (default both), `group`, `risk`, `batchable=1`, `q`, `requester`,
`order=urgency|created`, `limit` (1..100), `pending_before` / `history_before` (item-id cursors; `cursor`
applies to the selected side), and `history_status=approved|denied|answered|expired|withdrawn|failed|superseded`.
Bad values are 400 `invalid_request`.

- **Pending** always comes from the index. Default order is urgency: deferred last, soonest `expires_at`
  first (none last), higher risk first, oldest first. `order=created` is the previous newest-first order.
  A response adds `server_now_ms` and `counts` over the whole unfiltered pending set
  (`pending, batchable, deferred, by_group, by_risk`).
- **History** is the original newest-first page unless a filter is given (`group risk q requester
  history_status`), then it comes from the index. Logically expired items still count as history before
  they are materialized.
- `q` is a case-insensitive substring match over title, summary, requester and id. Not bodies.

## Expiry sweeper

`Inbox.Maintain` runs once at start (Reindex) and then every 30 s calls the hitl `ExpireDue`, which
materializes expired items with an outcome, an index update and an `item.expired` event (A5's stream).
Observable change (G7): expired items reach History at their deadline without a reader asking. Items that
were already expired when this ships are materialized on the first sweep.

## Migration

028 is the next free number on main (018-026) with Agents A3's 027 pending in #185; renumber at merge
if needed. It creates one table and three indexes and does not touch existing tables. The backfill runs on
start, not in the migration.
