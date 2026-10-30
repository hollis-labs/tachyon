# Chat backend contract (T0)

Source design: `tesseract:01M651VJ6DT88Q3YAJS9KJH6QJ`. The machine-readable contract is
`internal/chatapi/openapi.yaml`; fixtures are in `internal/chatapi/fixtures/`.

## Using the stubs

Run Tachyon with `TACHYON_CHAT_STUBS=1`. Every chat route (`/api/plugins/console/v1/chat/...`) and terminal route
(`/api/plugins/console/v1/terminal/...`) answers with a fixture in the final shape. Writes and terminal/workspace-content
reads still require an operator session; the stub changes data, not guards. Send `X-Stub-Status: 409` (any 400-599) to
get the error shape. `GET /chat/openapi.yaml` serves the contract. The terminal websocket answers 501 (frames are in the
spec). With the flag off nothing is mounted. Stub responses carry `X-Tachyon-Chat-Stub: 1`.

## thread_ref

`app:<app_agent_threads.id>` or `tether:<session_id>` (chatbridge grouping key: session_id, else local_session_id).
Tachyon-owned and stable; never the runtime session id. Parse with `chatapi.ParseRef`. It travels in path segments as is.

## Event envelope

Flat JSON per SSE `data:` line: `{"type", "thread_ref", "at", ...payload}`. `at` is unix ms. Types: message, delta, tool,
end, delivery (existing, unchanged); card, plan, activity, panel, topic, bookmark, workspace, context, thread,
terminal.run (new). The activity record's own type is `activity_type` inside an `activity` event.

Activity-class events (activity, plan, card, topic, bookmark, workspace, terminal.run) carry `id: <seq>` and are replayed
after `Last-Event-ID`; the rest are live signals, so after a reconnect refetch state. 15 s keepalive comments. A slow
subscriber is dropped and must refetch. Events are signals, not state.

## SSE replay

`scripts/chat-sse-replay -1` prints the stub stream and exits; `-l 3` resumes after id 3; `-r -` is the global spine.

## Conventions

snake_case JSON, unix-ms timestamps, errors `{code,message}` (400 invalid, 403 origin_denied|operator_session_required,
404 not_found, 409 conflict, 429 rate_limited, 503 not_configured), revision-checked writes answer 409 on a stale revision.

## Slice status

- T1 (threads) replaces these stubbed routes with real ones: `GET/POST /chat/threads`, `GET/PATCH /chat/threads/{ref}`,
  `GET/POST .../messages`, `POST .../read`, `GET .../events`, `GET /chat/events`. Other routes stay stubs until their slice.
- SSE ids come from a `chat_events` replay log (one monotonic seq across threads), written by `chatthreads.Hub.Publish`
  for activity-class types. When T6 adds `chat_activity`, an `activity` event's own `seq` field is the activity row seq and
  the SSE `id:` stays the event-log seq; clients resume with the SSE id only.
- Lenses: `agents` currently equals `all` (every thread is an agent thread); `work` = threads with a project_id or, from T2,
  a plan. Tether threads: title is the first turn's first line, `agent.address` is `tether://agent/<agent_id>` unless the id is
  already a URN, POST messages replies to the newest open replyable turn (409 `conflict` when nothing is waiting).
- `tether` thread `unread` counts turns newer than the last `POST .../read`; app threads use the existing read marker.
- T4 (workspace, scratchpad, settings) replaces those stubs: `GET/POST .../workspace`, `PATCH/DELETE .../workspace/{id}`,
  `GET .../workspace/{id}/content` (operator assurance on reads), `GET/PUT .../scratchpad`, `GET/PUT /chat/settings`.
  Upload errors: 413 `too_large` / `limit_reached`, 415 `type_not_allowed`. A `workspace` event `{op: add|remove|share|unshare, id}`
  is published on each change. Agent verbs `chat.workspace.list` / `chat.workspace.get` read only items the operator shared,
  for app threads only (preset `chat-workspace-read`; no existing preset gained them). Shared items go to the app agent as
  `client_context` v1 on each turn only when `TACHYON_CHAT_CLIENT_CONTEXT=1` (off until Nanite confirms the field and its size limit).
- T3 (topics, bookmarks): `GET/POST .../topics`, `DELETE .../topics/{id}`, `GET/POST .../bookmarks`, `DELETE .../bookmarks/{id}`.
  `message_ref` must be a message of that thread (400 otherwise); a topic's `seq` is resolved server-side for click-to-jump.
  POST bookmark is idempotent per (thread, message): 201 on create, 200 with the existing row on repeat (a repeat note updates it).
  Caps: 200 topics, 500 bookmarks per thread (413 `limit_reached`). Agent verb `chat.topic.add` (app threads, preset `chat-topic-add`).
- T2 (plan/todos): one plan per thread (`scope=thread`) and one per project (`scope=project`, resolved from the thread's
  `project_id`; 409 `no_project` when the chat has none). A todo list is a plan with `title: null`. `PUT .../plan` replaces the
  whole plan (`expected_revision` 0 to create; 409 on a stale one; `id`, if sent, must name the existing plan); steps keep
  their ids, steps you omit are removed, new steps get server ids. `PATCH .../plan/steps/{id}`, `POST .../plan/steps` and
  `POST .../plan/reorder` also need the plan's `expected_revision`; every change bumps the plan revision. A step linked to a Torque
  task (`task_ref`, set only by the Dispatch card in T5) follows the task's status: by hand it can only be parked (`blocked`) and
  unparked (`todo`), else 409 `linked_step`. `SyncTaskStatuses` is the one-way Torque sync (not scheduled yet). Agent writes
  (`chat.plan.get|write|step`, preset `chat-plan`) can propose and cancel but never approve, post a `kind: "plan"` message
  (`meta.plan_id`) to the thread and send a `panel` signal. Events: `plan {plan_id, revision, progress}`.
