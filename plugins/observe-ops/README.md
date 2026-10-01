# Observe polling contract

Other plugins' host operation and lifecycle metadata is now ingested privately
into the same bounded local rings. Observe reads and delivery never record
themselves. See [the host feed contract](../../docs/observe-host-feed.md) for
privacy, ordering, loss counters, private-command guards and restart/shutdown
limitations.

`POST /api/verb/observe_subscribe` describes stateless same-origin polling.
It allocates no subscription token or server-side state. For example:

```json
{"channel":"activity","filter":"{\"source\":\"nanite\",\"kind\":\"session_snapshot\",\"limit\":50}"}
```

The successful ResultEnvelope's `data` is:

```json
{
  "channel": "activity",
  "endpoint": "/api/verb/observe_activity",
  "method": "POST",
  "payload": {"source":"nanite","kind":"session_snapshot","limit":50},
  "transport": "polling",
  "supported": true,
  "mode": "snapshot",
  "poll_interval_ms": 2000,
  "max_limit": 200,
  "cursor_supported": false,
  "cursor": null,
  "durable_replay": false
}
```

Send `payload` as the JSON body to `endpoint` using `method`, with
`Content-Type: application/json`. The response is the standard ResultEnvelope;
`data` contains the selected channel's rows (possibly null when empty).
Poll no faster than the recommended `poll_interval_ms`, wait for each request
to finish, and stop polling when the view is inactive. The interval is client
pacing guidance, not a server-enforced rate limit. Reads remain `reads` verbs.

Supported channels map directly to existing host POST verbs:

| Channel | Endpoint | Accepted filter fields | Default limit |
| --- | --- | --- | --- |
| activity | `/api/verb/observe_activity` | source, kind, limit | 50 |
| logs | `/api/verb/observe_logs` | source, level, search, since, until, limit | 100 |
| events | `/api/verb/observe_events` | source, kind, limit | 50 |

`filter` remains an optional string containing a JSON object for compatibility
with the existing request DTO. Omit it for defaults. Limits must be integers
from 1 through 200; other fields must be strings. `since` and `until` are
RFC3339 timestamps; log boundaries are inclusive and search is a literal,
case-sensitive substring. Source/kind/level use exact matches. Unknown fields,
malformed filters, missing/unknown channels, and invalid limits/timestamps
return `status: "error"`, `error.code: "validation"`, without a descriptor.
Filters are capped at 8192 bytes.

Channels `metrics` and `status`, and any `since_id` filter on a supported
channel, return `status: "error"`, `error.code: "unsupported"`. Metrics and
status remain usable through their existing read verbs; they have no polling
subscription descriptor. The old `id`, opaque `filter`, and query-string
subscription URL in the response were stub fields and have been removed.

Activity and events combine current dependency probes and Nanite session
snapshots with the plugin's ephemeral telemetry. Logs contain only local
plugin logs, not provider logs. Each request reads a bounded snapshot;
clients replace the displayed snapshot instead of appending it as history.
Probe IDs repeat, timestamps can change, and ring entries can disappear.
No cursor, catch-up, live stream, durable replay, or Tachyon persistence is
promised. SSE and WebSocket transports are unavailable.

Capability and navigation declarations remain valid: `observe_subscribe`
now describes working polling, and each existing nav item gates on its own
read verb rather than on stream support. Clients must inspect the returned
transport metadata instead of inferring streaming from the verb's name.
