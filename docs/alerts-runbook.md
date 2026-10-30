# Alerts: running it, curl matrix, security notes (AL9)

Shapes are in `docs/alerts-contract.md` and `internal/alertsapi/openapi.yaml`; storage and lifecycle are in
`docs/alerts-store.md`. This page is the operator's view. Design: `tesseract:01M65CRJAF23AP7YBEBGJNE14M`.

## What runs

| Part | Where | Switch |
|---|---|---|
| Store, ops, counts, prefs, `/console/state` compat for alert keys | `register_alerts.go` | `TACHYON_ALERTS_STUBS=1` serves fixtures instead |
| Coalesce, escalate, snooze/defer wake (`Store.Maintain`) | started with the store | none |
| Approval alerts settle from the Approvals broker | started with the store | none |
| Stream hub, `GET /alerts/stream`, `GET /alerts/sources`, adapters (activity monitor, Agents bus) | `register_alertsfeed.go` | `TACHYON_ALERTS_SOURCES=0` leaves the adapters off; `TACHYON_ACTIVITY_URL` points the activity adapter |
| Forge `pr.*` events | forge index poller | `TACHYON_FORGE_STUBS=1` |

Not built: Torque and Tether-poll adapters, `GET /alerts/rules`, outbound sinks (AL7, review-gated). Task-done settling of task-failed alerts is not done.

## Behaviour to know

- Alerts are one shared operator state. Ids are stable (`al_...`); a redelivered event never re-creates or re-toasts.
- Coalescing: same coalesce key, open alert, inside the window (rule `windowSec`, else pref `coalesce_window_sec`, default 120, `0` off).
- Escalation: an unread active alert past its rule deadline rises to the rule's severity and needs you; reading it first cancels this.
- Snooze/defer wake on the server clock (checked at the next due time, at least every 30 s). `defer until_ref_change` wakes on an ingested event for that subject.
- Settling: approval alerts settle on `item.resolved`/`item.expired` (Approved, Denied, Expired); `alert.cleared` settles by coalesce key.
- Retention: terminal alerts 30 days, timeline events 7 days, pruned hourly.
- Rate limit: 200 events/min per source; the rest are dropped and one "N events dropped from <source>" alert is raised per minute.
- Sources health (`/alerts/sources`): `ok`, `degraded` or `down` with a note; `down` backs off to a 1 minute retry.

## Curl matrix

Alerts accept a browser session only. Reads need nothing special; writes need a same-origin `Origin` header. Use the browser's cookie
jar (`-b`) in practice; the headers below are what the server checks.

```sh
B=https://tachyon.example/api/plugins/console/v1/alerts
O='Origin: https://tachyon.example'

curl -s -b jar "$B?view=active&needs_you=1&limit=20"            # tray, Needs you first
curl -s -b jar "$B?view=history&q=deploy"                        # history, substring search
curl -s -b jar "$B/counts"                                       # bell badge
curl -s -b jar "$B/al_xxx"                                       # detail: timeline, technical, actions, approval block
curl -s -b jar -H "$O" -H 'Content-Type: application/json' \
  -d '{"op":"snooze","preset":"1h","expected_version":3}' "$B/al_xxx/ops"
curl -s -b jar -H "$O" -H 'Content-Type: application/json' \
  -d '{"op":"defer","until_ref_change":{"kind":"task","ref":"CW-20261030-0001"},"expected_version":3}' "$B/al_xxx/ops"
curl -s -b jar -H "$O" -H 'Content-Type: application/json' \
  -d '{"op":"settle","resolution":"Handled","note":"fixed","expected_version":3}' "$B/al_xxx/ops"
curl -s -b jar -H "$O" -H 'Content-Type: application/json' \
  -d '{"op":"read","scope":"all_active"}' "$B/ops/bulk"          # Mark all read
curl -s -b jar "$B/prefs"
curl -s -b jar -H "$O" -X PUT -H 'Content-Type: application/json' \
  -d '{"notify_level":"warnings","toasts":true,"quiet":false,"quiet_hours":{"enabled":false,"start":"22:00","end":"07:00","tz":"UTC"},"muted_sources":[],"coalesce_window_sec":120}' "$B/prefs"
curl -sN -b jar "$B/stream"                                      # SSE; add -H 'Last-Event-ID: 120' to resume
curl -s -b jar "$B/sources"                                      # ingest health
```

Expected refusals:

| Request | Answer |
|---|---|
| any write without a same-origin `Origin` | 403 `forbidden` |
| any call with an `Authorization` header | 403 `forbidden` (agents have no alert surface) |
| op with a stale `expected_version` | 409 `version_conflict` with the current alert |
| snooze without `preset` or `until_ms`, or beyond 30 days | 400 `invalid` |
| unknown alert id | 404 `not_found` |
| stream cursor older than the ring | one `resync` frame, then live frames; refetch the list |

## Security notes (relaxed posture)

- Alert ops are low impact, so there is no operator elevation or step-up; the guard is same-origin plus no bearer. This is the agreed
  posture for now, not a gap to close silently.
- Approve and Deny never go through alerts. An approval alert links to the item; the Approvals endpoints decide, and the alert settles
  from the broker. There is no confirmation token on alert routes.
- Event text is untrusted: plain text, capped (title 120, summary 300), control characters stripped. `primary.href` must be a same-origin
  path or http(s) URL. Technical details show top-level scalar payload values only, with secret-looking keys left out and token-looking values replaced.
- Every route is mounted by explicit pattern; the `/api/plugins/` catch-all is unguarded and must not serve alerts.
- Nothing leaves the process: no push, mail or webhook sink exists until AL7 is approved.

## Troubleshooting

- "Alerts are not updating": `GET /alerts/sources`; a `down` source shows its note. The activity adapter needs `TACHYON_ACTIVITY_URL` reachable.
- No toast for an alert: its frame has `toast:false` when Quiet, quiet hours, a muted source or the notify level applies; critical always toasts.
- A snoozed alert did not wake: the maintenance loop logs `alerts: maintenance failed`; wake is on the server clock, not the browser's.
- Approval alert stayed open: the broker drops slow readers and the follower re-subscribes after 1 s, without replay; settle it by hand or wait for the next change.
