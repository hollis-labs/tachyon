# Alerts store (AL1)

The durable Alerts store behind `/api/plugins/console/v1/alerts`. Contract: `docs/alerts-contract.md`
(AL0). Design: `design_tachyon_alerts_backend_20261030`.

## What is here

- `internal/alertstore`: `Store` (ingest, list, counts, detail, ops, bulk, prune), `API` (HTTP), `SettingsPrefs`,
  `StateCompat`. Migration `032_alerts.sql`: `alerts`, `alert_events`, `alert_seq`, `alert_source_cursor`.
- Wiring is in `internal/server/register_alerts.go` (start hook, explicit route mounts). `TACHYON_ALERTS_STUBS=1`
  mounts the AL0 fixture stub instead of the real routes.

## Behaviour

- **Ingest** (`alertsource.Sink`): the rule pack decides whether an event becomes an alert. A redelivered event
  (same dedupe key, `alert_events.dedupe_key` UNIQUE) is ignored. The alert id is `al_` + base32(sha256(dedupe key)).
  Text is cleaned and links must pass `alertsapi.SafeHref`.
- **Sequence**: every change bumps `alert_seq`; list and counts carry `seq`. `OnChange` is the stream hook (AL4).
- **Ops** (`POST /{id}/ops`): read, unread, dismiss, snooze (preset or `until_ms`, at most 30 days), defer, settle, reopen.
  `expected_version` is required; a stale one is `409 version_conflict` with the current alert.
- **Bulk** (`POST /ops/bulk`): read or dismiss, by `ids` (up to 200) or `scope: all_active`. A stale `expected_seq`
  is `409 seq_conflict` and nothing is applied.
- **Prefs**: one JSON row in `app_settings` (`alerts.prefs.v1`) behind the `PrefsStore` interface. Registering the
  setting in the settings registry (ST1/ST2) swaps the implementation, not the API. `POST /prefs/migrate` takes the old
  localStorage value once; if server prefs exist it applies nothing.
- **Retention**: terminal alerts 30 days, timeline events 7 days, pruned hourly.
- **Guard**: reads are open; writes need a same-origin browser request. Any `Authorization` header is refused
  (agents have no alert surface). No operator elevation: alert ops are low impact.

## /console/state compatibility

The console client keeps calling `/console/state`. `StateCompat` wraps the item-state handler:

- `PUT /console/state/al_...` becomes an alert op (read, dismiss, snooze, settle, reopen), taking the current
  version, and answers with an item-state row. Same-origin guard as above.
- `GET /console/state` adds one row per recent alert: active becomes `unread` or `read`; snoozed and deferred become
  `snoozed` with `snoozeUntil`; dismissed and settled keep their status.
- Every other key, and `POST /bulk`, goes to the item-state store with its operator guard. Bulk does not take alert keys.

## Coalescing, escalation, wake (AL3)

- **Coalesce.** An event whose rule gives a coalesce key folds into the open alert (active or snoozed) with the same key whose last
  event is within the window: `count` goes up, `last_at_ms` moves, and the event joins that alert's timeline. The window is the
  rule's `windowSec`, else the `coalesce_window_sec` pref (default 120, `0` = off). `read` is left alone unless the severity rises
  (then the alert is unread again and the change is `updated`, which toasts). A repeat after dismiss or settle is a new alert.
  Redelivered events are still ignored by the dedupe key.
- **Escalate.** An active, unread alert past `escalate_at_ms` gets the rule's `to` severity, `needs_you`, `escalated_at_ms` and a timeline
  entry; the change is `escalated` (a real re-toast). Reading or handling it first cancels this. The target is looked up in the current
  pack by `rule_id`; if the rule is gone or the alert is already that severe it is dropped quietly.
- **Wake.** `Store.Maintain` (started with the store) wakes snoozed and deferred alerts at `wake_at_ms` on the server clock: active, unread,
  timeline entry, change `woke`. It sleeps until the next due time, never more than 30 s. A deferred alert with `until_ref_change` also
  wakes when an ingested event carries that subject.
- `OnChange(id, seq, change)` now also reports `updated`, `woke` and `escalated`; the stream (AL4) turns those into toasts.

## Not here

The stream (AL4), humanised detail and approval linkage (AL5), prefs semantics (AL6). Detail returns an empty `technical`, `actions`
and `approval` until AL5. Ingest adapters are not started by the server yet, so coalescing runs on whatever feeds `Store.Ingest`
(today the forge poller).
