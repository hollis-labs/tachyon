# Agents collector (A1)

`internal/agentscollector` follows Tether's environment event stream and keeps a derived, rebuildable
read model (migration 024). Design `tesseract:01M6547NTB3FR4P49GQRP8WC4B` section 13 (A1), decision E1.
Derivation (state, one-liners, context) is A2; the bus feed is A4; the sessions API and SSE are A3.

- **On by default** when a Tether address is set; `TACHYON_AGENTS_COLLECTOR=0` turns it off. Uses the
  usual `TETHER_TOKEN_FILE`, which must be read scope only. A token over plain http to a non-loopback host
  is refused (`upstreamauth.Guard`).
- **Sources**: `GET /environment/snapshot` (strategy A), else `GET /sessions?state=running` + the newest
  event seq from `GET /events?limit=1` (strategy B, recorded in the status). Live: `GET
  /environment/events?after_seq=N`. Backfill: `GET /events?since_seq&cursor&limit` newest first, at most 20
  pages. All wire shapes are UNVERIFIED against a live Tether and are tested with recorded frames.
- **Gaps**: a stream `gap` frame, or a cursor older than 2 minutes at start, records a row in
  `agents_gaps`. History fills it when it can (`backfilled=1`); otherwise it stays open and is reported in
  `GET agents/collector` (`open_gaps`). Nothing is silently filled. An unreachable Tether shows
  `collecting: false`, never an empty healthy page.
- **Resilience**: backoff 3 s doubling to 1 min, a 60 s watchdog (no frame or keepalive reconnects), panic
  recovery that never prints the panic value, idempotent apply (`tether_event_id` unique), the cursor
  moves in the same transaction as the rows, a bounded queue (4096) and 1 s batching.
- **Privacy**: `session.output` payloads are never parsed (counted, and used as an activity time only).
  Other payloads are read for an allowlist of identifier fields; stored summaries are built from the kind
  and those fields, never from payload text. A test plants a secret in output, tool args and message
  bodies and greps every table.
- **Retention**: events 7 days, gaps and ended sessions 30 days, pruned in batches of 1000 every 10 minutes
  with a receipt in `agents_collector_state`. Samples (downsampling) arrive with A2.
- **Not here yet**: `dropped_output_frames` stays 0 until A2 parses output in memory and can shed it under
  load; strategy B does not call per-session health; bus topics report `off`/`no_emitter` until A4.

## Derivation (A2)

`internal/agentsderive` is pure (no I/O) and read-time: `Derive(Input, now, staleAfter)` computes state when
a session is read, so changing `stale_after_s` takes effect on the next read with no backfill.

- **Stale** (addendum 1): working or waiting with no event for `stale_after` (default 15 min), or dead
  (`alive` false, `orphaned`, process gone) -> `not_alive`, or worker offline -> `worker_offline`. Idle is
  never stale by age; a quiet idle session keeps `last_activity_at`. A stale waiting session keeps `needs_you`.
  `alive`, `worker_online` and Chimera's process-gone are stored columns A3 or A5 fill; until then they are
  unknown and only the no-events rule applies.
- **Output (E3)**: the collector hands each `session.output` payload to a sink in memory. `ExtractOutput`
  reads Claude stream-json lines (tool_use name and a few named fields, message usage, result
  `contextWindow`, `compact_boundary`) and returns derived values only. Bash yields its first token and
  nothing else; every line passes the secret redactor and is cut to 160 characters. Unknown or cut-short
  lines are skipped. Context = input + cache read + cache creation of the last call; the window falls back
  to a model table. No source exists for a context breakdown and none is invented (`breakdown` stays null).
- **Markers**: a real `compact_boundary` is recorded as not inferred; a drop over 40% is an inferred
  compaction; falling under 5% of the window is an inferred clear.
- **Tokens** come from sanitized `session.usage` events (per finished turn). A session with none has NULL
  tokens (not metered), never 0. Samples: per-minute buckets kept 48 h, hour buckets 30 d.
- Not done here: `alive`/`worker_online` producers, tool-run grouping and the sparkline are helpers
  (`GroupRuns`, `Spark`) that A3 calls when serving the API. The sink saves at most once a second per
  session, so the last figures of a turn can lag until the next output.

## Bus feed (A4)

App-published events on the same stream (the `app` field) are normalised into `bus_events` (migration 026)
and served at `GET agents/bus/events?before_seq&limit<=100&type=deploys|restarts|tasks|failures&app&q`.

- Mapping: `deploy.state_changed` -> deploy (failed = error, rolled_back = warn); `resource.state_changed`
  with reason `restart` -> restart; `fleet.node.*` -> host; `alert.raised|cleared` -> failure;
  `task.state_changed` -> task (a closed task with a PR in its payload gets a `review_ref`);
  `workstream.created` -> workstream; `card.*` -> card; Tether's own `session.started|ended|resumed|restarted`
  -> lifecycle. These kind names come from the catalog and are not verified against live traffic.
- The subject is a sentence assembled from the kind and validated names (`[A-Za-z0-9._:/@? -]`, task ids,
  `owner/repo#N` PR refs). Titles are redacted and cut. No other payload text is copied.
- `GET agents/collector` reports `bus_topics` from the data: `live` (an event of that type in the last 7 days),
  `off` (none seen; an export may be disabled: Cerberus export, `TORQUE_EVENTS_HUB`), `no_emitter` for host and
  workstream until something emits them. The feed degrades visibly instead of looking empty and healthy.
- Retention 7 days. The `bus` SSE event and replay arrive with A3.

## Sessions API and stream (A3)

`internal/agentsserve` serves the read side from the collector's model (it needs the collector, so it is
mounted only when the collector runs). Real routes win over the stub when both are on:

- `GET agents/sessions` (`state`, `q`, `group=workroot|agent|none` as a sort key, `include_ended`), `sessions/{id}`,
  `sessions/{id}/events` (`filter`, `q`, `before_seq`, `limit`, `group_runs`), `context-series`, `tokens`,
  `history`, `GET/PUT agents/settings`, `GET agents/stream`. Controls (nudge, restart, skip) are not here (A6).
- State is derived **at read time** (`agentsderive.Derive`), so a change to `stale_after_s` applies on the next
  request and a quiet session turns stale without any new event.
- Honest nulls: no metered usage means `tokens.metered=false` with null counts, `context` null with
  `source: "unknown"`, `one_liner` null. `capabilities` are all false until A6. `assignment` is null until A5.
  `kind` is always `process` (the chat roster join does not exist yet). `needs_you.since` is the last event
  (Tether does not say when the wait began). `breakdown` is always null.
- Agent identity (`agent.urn`, `logical_agent_id`, name) and lineage (`parent_session_id`, `replaced_by`)
  come from payload fields `agent_urn`, `logical_agent_id`, `agent_name`, `parent_session_id`, `replaced_by`
  **when Tether sends them** (names unverified against live traffic). Without them the name is "session <id>"
  and `history` is empty.
- Timeline: the first half of a tool pair (`tool.call.started`) is hidden; `tool.call.finished` is "Used <tool>".
  `group_runs=1` folds tool calls within 30 s into a `tool_run` (a run that crosses a page edge is split).
- Tokens: `hour` is the sum of the last 60 minute buckets; `share` is this session's input+output over every
  session's for that hour; `trend` compares the last 30 minutes with the 30 before (20% dead band).
- Context series reads minute buckets (48 h) and hour buckets before that, thinned to 600 points. `step=turn`
  is the same as `min`: a sample is taken whenever usage is seen.
- Stream: `agents_stream` (migration 027) holds `bus`, `gap` (and later `control`) events in one id sequence,
  written in the same transaction as the fact. `session`, `summary` and `collector` are live snapshots found by
  polling the model once a second while a client is connected (it also announces time-driven stale
  transitions). A `session` event carries the full Session. A slow client is dropped and reconnects; a
  `Last-Event-ID` older than the oldest retained row gets a `gap` (`purged`) so it refetches. `?once=1` ends
  after the replay or snapshots (for scripts).
