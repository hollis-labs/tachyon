# Observe pages

The shell's observability navigation offers **Activity**, **Logs** and
**Metrics**, registered as `/observe`, `/observe/logs` and `/observe/metrics`.
These are shell navigation identifiers within the UI served under `/sysop/`.
The pages show current observations and retained records, not durable history.

## Capabilities and views

| Surface | Required verb |
| --- | --- |
| Activity navigation and snapshots | `observe_activity` |
| Events tab on Activity | `observe_events` |
| Logs navigation and snapshots | `observe_logs` |
| Metrics navigation and retained samples | `observe_metrics` |
| Status section on Activity | `observe_status` |
| Optional activity/events/logs auto-refresh | `observe_subscribe`, plus the selected read verb |

Activity and Events offer exact source/kind filters and a record limit. Logs
offer source, level, case-sensitive message search, time range and limit.
Logs cover local and host-recorded logs, not every provider, model output or
conversation. Metrics offer comma-separated exact names, one tag key/value
filter, time range and limit. Record limits must be integers from 1 to 200.

Metrics display supplied values, units, timestamps and tags. Missing or invalid
measurements are unavailable, not zero; a missing unit is not invented. These
are direct reads of retained samples. The UI does not promise continuous
series, token usage, cost accounting or OpenTelemetry history.

## Refresh and polling bounds

Entering a view, applying filters or pressing **Refresh** performs a read.
Metrics and Status use manual reads only. Activity, Events and Logs offer
**Auto-refresh snapshots** when `observe_subscribe` is available; it starts
off. The subscription response is a validated polling descriptor, not a
stream connection or registered subscription.

The client accepts only the selected channel's same-origin POST endpoint,
snapshot mode, polling transport, supported limits and allowlisted filter
fields, with no cursor or durable replay. It then invokes that channel's
read verb. An unsupported or invalid descriptor leaves manual Refresh
available; the browser never follows an arbitrary descriptor URL.

Reads for the same channel are serialized, including across filter changes.
Each successful read replaces the snapshot rather than appending history.
The delay after a response is never below the descriptor's interval. Error
backoff is capped at 30 seconds unless the descriptor requires a longer
interval. Auto-refresh pauses after five consecutive failed snapshot reads
or ten minutes, on capability loss, or for an operator decision. Leaving the
view clears scheduled polling, and old replies cannot update a newer view.
There is no streaming, cursor pagination or durable replay.

Loading and refresh states are distinct. A failed read preserves the previous
snapshot and marks it stale; it does not turn an error into an empty result.
Read-only details render data as literal text. Asks use the shared
[operator-decision panel](hitl-ui.md), without automatic continuation.

## Reading Status honestly

Status distinguishes the Observe aggregate health value from dependency
reachability. A reachable dependency does not establish workload health.
**Active agents** is always **Unknown — not measured**. Nanite sessions with
status `active` are counted only when the response says the count is known;
active status does not mean executing. Uptime belongs to the Observe process.
The section also shows the response's last-updated timestamp.

`error_count` represents local error events and plugin failures in the retained
ring. `operation_error_count` represents operation errors in that ring,
separately from health. Neither is a lifetime error total.

An optional host-feed receipt shows epoch, last received sequence and delivery
counters. Its absence is **No host-feed receipt reported**, not proof of zero
activity or zero loss. Counters are the last received report and may lag host
accounting. Delivered, operation overflow, lifecycle overflow, unavailable and
failed-delivery values count records. Queue-wait timeouts count attempts,
not record loss, and are not added to a loss total. Accepted records may be
visible even when their acknowledgment was lost. There is no exactly-once
guarantee or complete provider-history claim.

Source: [Activity](../../frontend/src/pages/observe.tsx),
[Logs](../../frontend/src/pages/observe-logs.tsx),
[Metrics](../../frontend/src/pages/observe-metrics.tsx),
[shared reads and Status](../../frontend/src/components/observe/observe-shared.tsx),
[API validation](../../frontend/src/api/observe.ts), and
[host feed contract](../observe-host-feed.md).
