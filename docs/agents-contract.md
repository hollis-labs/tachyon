# Agents monitor contract (A0)

Final shapes for the Agents monitor backend: `internal/agentsapi/openapi.yaml`, fixtures in
`internal/agentsapi/fixtures/`, design `tesseract:01M6547NTB3FR4P49GQRP8WC4B` (+ Addendum 1
`tesseract:01M65493M4ES4ZZ309AJV5JCDY`). Run the stub with `TACHYON_AGENTS_STUBS=1`; replay the stream
with `scripts/agents-sse-replay [-l ID] [-1]`.

- Prefix `/api/plugins/console/v1/agents`. GETs are open. Every write (nudge, restart, skip, settings PUT)
  needs an operator session; restart will also need operator-token assurance in the real build.
  Control routes are not agent verbs.
- Errors are `{code,message}`. `X-Stub-Status: 400|404|409|429|503` forces an error from any stub route;
  `X-Stub-Scenario: collector_down` shows an unreachable Tether on `/collector` and `/sessions`.
- **Never fake zero.** Unmetered sessions have `tokens.metered=false` and null counts; sessions without
  context data have `context.used/window/pct = null`, `source: "unknown"`. `context.breakdown` is always
  null (no source exists).
- **Stale** (`state: "stale"`) always carries `stale_reason`: `no_events` (none for `stale_after_s`,
  default 900), `not_alive`, `worker_offline`. A stale session that is also waiting keeps `needs_you`.
  Idle sessions show `last_activity_at` ("idle 5d").
- Compaction and clear markers carry `inferred` when they come from the context-drop heuristic.
- Timeline: newest first; consecutive tool events within 30 s are grouped into a `tool_run` when
  `group_runs=1`; `gaps` lists collection gaps inside the range. `payload` is always null: raw model
  output and tool arguments are never stored or returned.
- Stream: flat JSON `{type, at, ...}`. `bus`, `control` and `gap` carry `id:` and replay after
  `Last-Event-ID`; `session`, `summary`, `collector` are live snapshots. After a `gap` or a reconnect that
  replay cannot cover, refetch `GET /sessions`. 15 s keepalive.
- Controls: nudge `queued | delivered | interrupted_then_delivered | failed`; restart `requested ->
  [handoff_sent -> waiting] -> stopping -> stopped -> resuming -> done | failed(step)`. An interrupt that
  cannot be done answers 409 `interrupt_unsupported` (no silent downgrade); the client offers "send without
  interrupt". Restart discards pending approvals (`discarded_pending`); it cannot keep them.
- `collector.bus_topics` says which Activity topics can ever have data today (`live | off | no_emitter`).

Stub limits: nothing is stored; control state comes from fixtures; filters are minimal (`state`, `q`,
bus `type`).

## Assignment join (A5)

`Session.assignment` is real for sessions Tether launched with a task (`agents_sessions.task_ref`). `internal/agentsassign` finds the
Planner theme that holds the task (direct `tasks` plus its workstreams' Torque roll-up), then returns `index` (1-based, task ids sorted)
of `count` (tasks not cancelled, abandoned or archived) and `stats.done`. `time_on_task_ms` runs from the first session that worked the
task (to now while any is live, else the last end); `events` counts the collector's derived events for those sessions. Cached 60 s per
session. It is `null` when the session has no task, no theme holds it, the task is cancelled or unknown to Torque, or the Planner or
Torque cannot be read. It needs `TORQUE_URL`; reads only.
