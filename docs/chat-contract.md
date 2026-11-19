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

Activity-class events (message, delivery, activity, plan, card, topic, bookmark, workspace, terminal.run) carry `id: <seq>` and are replayed
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
  Caps: 200 topics, 500 bookmarks per thread (413 `limit_reached`). Agent verbs `chat.topic.add` (app threads, preset `chat-topic-add`) and the read verbs `chat.topic.list`, `chat.bookmark.list` and `chat.approvals.list` (same preset and thread rule; they return `{topics}`, `{bookmarks}` and `{approvals: [{item_id, kind, state, message_ref, created_at}]}`, the last being the open approval-like cards behind the Approvals panel badge; read only, an agent cannot answer them). Schemas: every input field carries a `description` (Go struct tag `desc:`), and every chat and terminal verb description has a "Use when" sentence; a test enforces both. `chat.panel.open` returns `{ok:true}` once the hint is sent; `ref` is recorded in the event but the screen does not scroll to it yet.
- T2 (plan/todos): one plan per thread (`scope=thread`) and one per project (`scope=project`, resolved from the thread's
  `project_id`; 409 `no_project` when the chat has none). A todo list is a plan with `title: null`. `PUT .../plan` replaces the
  whole plan (`expected_revision` 0 to create; 409 on a stale one; `id`, if sent, must name the existing plan); steps keep
  their ids, steps you omit are removed, new steps get server ids. `PATCH .../plan/steps/{id}`, `POST .../plan/steps` and
  `POST .../plan/reorder` also need the plan's `expected_revision`; every change bumps the plan revision. A step linked to a Torque
  task (`task_ref`, set only by the Dispatch card in T5) follows the task's status: by hand it can only be parked (`blocked`) and
  unparked (`todo`), else 409 `linked_step`. `SyncTaskStatuses` is the one-way Torque sync (not scheduled yet). Agent writes
  (`chat.plan.get|write|step`, preset `chat-plan`) work on `app:` and `tether:` threads alike. `thread_ref` is optional. Given, the thread must exist (404) and belong to the caller (403). Omitted, the call is tied to the caller, never guessed: through the **pinned Tether proxy** it is the thread of the session the proxy is acting for; for a direct credential it is the credential's only open (non-archived) thread; otherwise the verb fails with an error saying `thread_ref` is required. **Pinned Tether proxy:** one service identity can be pinned as the trusted Tether MCP proxy (`tachyon access pin-proxy --identity <uuid>`; `tachyon access unpin-proxy` clears it; the pin is one row in `service_proxy_pin`, applies to the identity's existing tokens at once, and shows as `"proxy": true` in `tachyon access list`). Only a request authenticated as that identity is believed when it sends `X-Tether-Session-Id` (the Tether session the call is made for; see Tether's [trusted session context](../../tether/docs/trusted-session-context.md)); from every other caller the header is ignored. Through the proxy the calling agent is the one on the session's own thread (`tether:<session>`), so no per-agent binding or `thread_ref` is needed: the proxy may touch only `tether:<session>` (any other `thread_ref` is 403, so one agent's session cannot reach another agent's plan), a call with no session id is refused, and a session with no thread yet is an error. The proxy credential holds only the verbs listed below. **Other agent chat verbs through the proxy:** `chat.workspace.list|get`, `chat.topic.add`, `chat.activity.append` and `chat.panel.open` follow the same rule: through the pinned proxy `thread_ref` is optional and defaults to `tether:<session>`, any other `thread_ref` is 403, no session id is refused, a session with no thread is an error. Direct credentials are unchanged (`thread_ref` required, `app:<id>`). A proxy token for them needs exactly these verb names: `chat.plan.get`, `chat.plan.write`, `chat.plan.step`, `chat.workspace.list`, `chat.workspace.get`, `chat.topic.add`, `chat.activity.append`, `chat.panel.open` (`tachyon access issue --verbs ...`). `terminal.*` is not included. Every verb's schema description carries a "use when" sentence and the `thread_ref` rule.  For direct (non-proxy) credentials a credential belongs to a thread through an **explicit binding**: the identity is bound to one or more agents (`tachyon access issue --agent <id>[,<id>]` when issuing, or `tachyon access bind --identity <uuid> --agent <id>[,<id>]` for an identity already issued; `tachyon access unbind` removes one; bindings apply to the identity's existing tokens at once and show in `tachyon access list`). A thread is the agent's when its agent id or address (`tether://agent/<id>`, `msg://agent/<mux>/<id>` and bare `<id>` are the same) equals a bound agent. Fallback only, for credentials issued before bindings: identity name or id equal to the thread agent's id or the thread's own id. A `tether:` plan posts no chat message (only app threads have a message log) but fires the same `plan` and `panel` events, so the panel refreshes. Agent writes can propose and cancel but never approve, post a `kind: "plan"` message

**Plan carry-over (restart, reboot, crash).** A plan belongs to the conversation, not to one session: when a Tether session is replaced by a relaunch of the same logical agent, its plan follows automatically, with no agent action and no `thread_ref`. The pinned proxy also forwards `X-Tether-Logical-Agent-Id` (the verified logical agent) and, only when the session continues another, `X-Tether-Continues-Session-Id` (Tether's `continues_session_id`); both are believed only from the pinned proxy identity, exactly like `X-Tether-Session-Id`, and ignored from every other caller. On a session's first plan verb Tachyon records `(session, agent, continues)` in `chat_session_lineage`; if the session has no plan of its own it walks `continues` up the recorded chain (at most 16 hops) and **moves** the nearest ancestor's plan to `tether:<session>` in one transaction (steps and Torque links kept, revision bumped once, a `plan.carried` activity row, and the usual `plan`/`panel` events so the panel refreshes). It is a move, not a copy; a session that already has a plan is left alone (no merge). A fresh boot (no continues header) carries nothing and never touches an older plan. Every recorded hop must belong to the same logical agent, otherwise the carry is refused and logged; an ancestor never seen through the proxy (a session from before this existed) cannot be verified and is not carried, so its plan stays under its own thread. With the logical agent header a session may use plan verbs before its first turn, and a `tether:` thread that holds a plan but has no turns is listed in the thread list (title `Session <id>`, agent from the lineage record). `DELETE /threads/{ref}/plan` (operator) clears the plan and, for a `tether:` thread, writes a clear marker on its lineage so older plans are not resurrected through it by a later replacement; deleting the thread's data does the same. If Tether cannot establish a parent, behaviour is a clean start and the old plan stays recoverable under the old thread.

  (`meta.plan_id`) to the thread and send a `panel` signal. Events: `plan {plan_id, revision, progress}`.
- T6 (activity, panel events, context meter): `GET .../activity?before_seq&limit<=100&type&q&actor` (newest first, `has_more`),
  `POST .../activity/comments {body<=2000}` (always `actor: "user"`; any other field is a 400), `GET .../context`. `chat_activity`
  is append-only and keeps the newest 5000 rows per thread; its `seq` is the feed cursor (the SSE `id:` of an `activity` event comes
  from the T1 event log, so the two differ; the event carries the feed `seq` as a field). Payloads are mini-cards `{title<=120,
  summary<=200, review_ref}` (comments: `{body}`), redacted and bounded; `cite` is at most 8 of `{kind: task|pr|file, ref, label}`.
  Written by Tachyon at these seams: `tool.call` (runtime tool end), `plan.changed`, `topic.added`, `bookmark.added`, `file.shared`,
  `panel.opened`. `card.answered` and `approval.decided` are written when a card settles from either the thread or the global queue (CW-20261112-0071); NOT written yet: `card.sent`, `task.closed`
  from Torque (phase 2; agents can write `task.closed` and `note` through the verb). Actors: `user`, `agent` (Tachyon saw the
  agent act but not which one), `agent:<credential id>` (verb writes). A feed write that fails never fails the change behind it.
  Agent verbs: `chat.activity.append {thread_ref, type: task.closed|note, title, summary, cite, review_ref}` (preset
  `chat-activity-append`; actor and source are forced to the credential, 30 per minute per thread) and `chat.panel.open {thread_ref,
  panel, ref, reason}` (preset `chat-panel-open`; panels plan, topics, bookmarks, work, activity, approvals, files, doc; 6 per
  minute per thread, 409 over that). A panel event is a hint only: `{panel, ref?, reason, source: agent|system}`.
  Context meter: `{used, window, model, source: runtime|unknown, breakdown: [], at}`. The numbers stay `null` and `source`
  `unknown` until the thread's runtime reports a meter. Nanite (deployed 2026-10-30, CW-20261030-0010) sends `usage.{used,window,model}`
  on `run.finish` and usage events: Tachyon stores the latest report per thread and sends a `context` event. Tether threads are always `unknown`.

## Quick terminal (T8, real)

The terminal routes are real; with `TACHYON_CHAT_STUBS=1` the stub still answers the routes that are not mounted
(the websocket is real and the stub's 501 never shows). Every route needs the operator session, reads included, and a
request carrying an `Authorization` header is refused 403: an agent credential cannot approve, edit, stop or close.

- **Credentials** are the Tachyon host user's own `~/.ssh/config` and ssh-agent. Tachyon never reads a key file. The picker
  lists machines reachable from the Tachyon host; the client sends `machine_id`, never a host name.
- **Machines and rules** live in a JSON file (`TACHYON_TERMINAL_POLICY`, default `<data dir>/terminal-policy.json`, reloaded on
  change): `{"machines":[{"id","label","host","user"}], "rules":[{"id","machine":"*|id","exact"|"prefix":[words],"decision":
  "allow|ask|deny"}], "ssh_auth_sock"}`. Exact `Host` aliases of `~/.ssh/config` are listed too. Default is ask; deny beats
  allow; an allow never applies to a command with shell metacharacters. A file that does not parse means nothing is allowed.
- **Agent flow**: verb `terminal.propose {thread_ref, machine_id, command, reason}` (preset `terminal-propose`, which also grants
  `terminal.machines` and `terminal.run.get`). A message `kind=run` `{run_id}` is added to the stream at once. Policy `allow`
  starts the run with `approval.mode=policy`; `deny` is refused; otherwise the operator answers with
  `POST /terminal/proposals/{id}/run|deny|edit`. Edit makes a new proposal (new digest, always asks) and supersedes the old
  one. `run` re-derives the digest from the stored bytes and refuses with 409 `digest_mismatch`. Proposals expire after 15 minutes.
- **Exec** is `ssh -tt` with a fixed option template (`StrictHostKeyChecking=yes`, no forwarding, `BatchMode=yes`), so output is
  the merged pty stream. Limits: 10 minutes, 4 live runs, 1 MiB live ring, 64 KiB stored tail. Output is ANSI-stripped and
  redacted per line before events, storage and the agent. Stop is SIGTERM then SIGKILL after 5 s to the local ssh; it does not
  promise the remote command died.
- **Interactive**: `POST /terminal/sessions` then the websocket at `ws_path` (same origin). First frame should be the resize
  text frame; ssh starts at that size. Binary frames are pty bytes. 4 sessions, 30 minute idle. Only byte counts are stored.
- **Not built** (deferred by the owner): the `stale` read-only state on elevation lapse, an approvals-queue card per proposal
  (`item_id` is always null; the run block is the approval surface), `kind=machine` objects in the picker, ControlMaster.


## Agent Panel (multi-panel host)

The chat sidebar is the **Agent Panel**: a host that shows one *panel* (a named set of tabs) per thread. Client-side registry: `plugins/console/ui/src/chat/aside/registry.ts`.

- `PanelDef { id, label, owner: 'system' | 'user', tabs: { id, label, render? }[], appliesTo?: { agents?, roles? } }`.
- The existing tabs (Plan, Topics, Bookmarks, Work, Activity, Approvals, Files, Doc) are the default panel, id `agent`, always available. The id is reserved.
- `registerPanel(def)` adds a panel (plugins call it at load; user-defined panels use the same call with `owner: 'user'`). A tab with `render` draws its own body; built-in tab ids are drawn by `Aside`.
- Which panel a thread shows: the user's stored pick (`localStorage` `console.agentPanel.<thread_ref>`) if it still applies; otherwise the first registered panel whose `appliesTo` names this agent; otherwise the default. When more than one panel applies, a selector in the panel header switches between them.
- Data model decision: the registry is client-side (plugin-registered) for now. Server-side per-user panel config is a later step; it would feed `registerPanel` and change nothing in the host.
- `panel` server frames still name a tab id; they apply when that tab is in the open panel.

### Planner panel (Portfolio Manager)

`chat/aside/PlannerPanel.tsx` registers panel `planner` with tabs **Board** (in-flight and cleared themes with segmented progress, next step, blocked and decision chips; `GET /planner/board?scope=themes`) and **Need you** (`GET /planner/need-you` reasons, `GET /planner/decisions?status=proposed`). It applies to agent ids in `PLANNER_AGENTS` (`portfolio-manager`, `planner`, `pm`); add the roster id there if the Portfolio Manager uses another. Read-only; each tab links to the full Planner. Agent control of the panel waits on the agent-control task.

### Artifacts tab

The Doc tab is **Artifacts** (tab id stays `doc`). `chat/aside/ArtifactsPanel.tsx` lists the thread's workspace items not added by the user (`GET /threads/{ref}/workspace`); opening one replaces the list with an inline preview until Close/Esc: html in a script-less sandboxed iframe, images, video and audio from authenticated blob URLs, markdown rendered, text in a block, other types a download link. Host-path refs open in the File viewer. A document opened from Files still shows in the full-window Doc view.

### Agent-driven panels (Once | This session | Always)

The aside has no auto/manual mode and no chip. The agent may open a panel at any time (`chat.panel.open`, or the plan/approval signals). If the user changed the view since the agent last did, the agent's next change is held and the aside asks: **Once** (this request), **This session** (until the page closes), **Always** (kept in this browser), or **Not now** (dropped). Grants are scoped to one agent in one thread (`<agent>|<thread>`; `chat/aside/grants.ts`). Settings > General > Agent Panel lists the Always grants with Reset. Typing in an aside input still blocks any switch.

Verb result: `chat.panel.open` returns `{ok:true}` when the hint is sent. That means delivered, not applied: the screen applies, holds (pending) or drops it, and the server does not see which. A held request stays until the user answers or a newer request replaces it. Server-side granted/denied/pending results would need the browser to report back; not built.

### Derived agent activity (CW-20261112-0069)

For a `tether:<session>` thread the Activity feed also fills from the session's own tool calls, not from the agent. The Agents collector (which follows Tether's environment stream) hands each stored batch of live `tool.call.finished` frames to an observer (`Collector.SetObserver`); `chatactivity.Classify` keeps only the sanitized tool name's meaning and drops the rest as noise: `message.sent` (tether_message send/reply/post/notify, group post), `message.received` (inbox, consume, list, get, thread, group read), `task.checked` (torque get/list/search/facets/progress), `task.updated` (torque comment/update/transition/create), `reminder.set` (CronCreate, ScheduleWakeup), `wait` (Monitor, Sleep). Arguments are never read or stored. A repeat of the same type on a thread inside 30 s is dropped (`Throttle`), so polling is one row. Frames older than two minutes (catch-up after a restart) and backfill are ignored. These types are Tachyon-written: agents still append only `task.closed` and `note`. The Activity tab's Filters chips pick one type. Retention is the feed's own (5000 rows per thread). Pull from Tether history is not used; the live stream is the source.

### Multiple plans per thread (CW-20261112-0067, backend)

A thread may hold up to 20 plans (migration 058 drops the one-per-thread index and adds `position`; every existing plan became plan 1). A plan carries `position`. Routes (operator): `GET /threads/{ref}/plans` → `{plans}` in order; `POST /threads/{ref}/plans {title?}` → 201 a new empty plan after the others; `POST /threads/{ref}/plans/reorder {plan_ids}` (every plan once; revisions do not change); `DELETE /threads/{ref}/plans/{id}?expected_revision=` (409 on a stale revision, 409 `linked_step` if a step is linked to a task); `POST /threads/{ref}/plans/{id}/steps/{step}/move {to_plan, position?, expected_revision, to_expected_revision}` → `{from, to, step}` (keeps the step id, status and `task_ref`; both revisions must match and both advance; 409 `conflict`, 404 unknown step, 400 same plan). Each side of a move, a create, a delete and a reorder publishes `plan` and writes `plan.changed`. Plan carry-over moves every plan of the ancestor thread, in order. Thread progress is the sum over the plans. **Verbs.** `chat.plan.get` and `chat.plan.write` take an optional `plan_id` (default: the first plan); `chat.plan.step` takes `plan_id` too; new `chat.plan.list`, `chat.plan.create {title?}` and `chat.plan.move_step {plan_id, step_id, to_plan_id, position?}`. Step routes also exist per plan: `/threads/{ref}/plans/{id}/steps[/{step}]`. **Screen.** The Plan tab (`chat/aside/PlansPanel.tsx`) is an accordion, one section per plan; open state is remembered per thread in this browser. Plans reorder by dragging the grip or with the up/down buttons; a step moves by dropping it on another plan or with its "Move to…" select. **Kept:** the single-plan routes (`/plan`, `/plan/steps`, `/plan/reorder`) still act on the first plan because the inline plan card in the chat stream (`cards/useBlocks.ts`) uses `/plan`.
