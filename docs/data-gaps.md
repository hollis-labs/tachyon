# Torque GUI → Tachyon v2: DATA GAPS

This is the **original Torque GUI port inventory**, not the current Tachyon
ownership or agent-access contract. Planner now uses Tachyon-owned storage,
schemas, mutations and change events. Torque supplies execution-task enrichment.
For current routes, agent grants and MCP/CLI setup, see the
[Planner migration](../README.md#portfolio-planner-migration-and-local-maintenance)
and [agent access](../README.md#planner-access-for-agents) sections. `LIVE` below
describes the source inventory; it does not mean those Torque routes should be
used for current portfolio planning.

Repo: `/home/chrispian/dev/hollis-labs/apps/torque` (GUI at `apps/gui/src`). Routes are cited as `server.go:L<n>` (`internal/httpserver/server.go`); `/api/v1` prefix is implied.
Target: Tachyon v2 plugins **`planner`** and **`console`**, with **no mock/fixture data** ported.

## Legend

| Tag | Meaning |
|---|---|
| **LIVE** | A real Torque endpoint serves it today (route cited). |
| **RELAY** | A Torque route exists but only proxies another service (named). |
| **PARTIAL** | A real endpoint exists but misses fields or semantics the UI needs; the UI derives or degrades. |
| **GAP** | Mock, fixture, localStorage or in-memory only. No backend. |

What gets dropped in the port (do **not** bring these over): `console/*/mock.ts`, `fixtures.ts`, `lifecycle-fixtures.ts`, `torque-fixtures.ts`, `samples.ts`, `fixture-source.ts`, `fixture-families.ts`, `cards/mock-handlers.ts`, `hooks/use-agent-activity.ts` (demo seeds), the demo catalogs in `lib/agent-builder.ts`, `MemoryStateStore` seed data, and the `MockIdentityConfirm` "auto-grant" mode.

Existing transport facts the implementer should know:
- Torque SSE `GET /events` (`server.go:L348`, `sse.go`): JSON `{type, data, timestamp}`. **No event `id`, no cursor, no `Last-Event-ID` resume.**
- Activity relay (`activity.go`): `GET /activity/agents` proxies Chimera `http://127.0.0.1:8099/api/plugins/activity-monitor/agents`; `GET /activity/stream` proxies `/api/stream` (passes only `since`, `kind`, `Last-Event-ID`). Override with `TORQUE_ACTIVITY_URL`. Gap frames carry no detail and there is no `epoch`.
- Messaging: go-messaging store in Torque. `GET /messages/inbox` **drains** (destructive). `GET /messages/subscribe?to=<urn>` is a working SSE (`event: message`) that the GUI does **not** use yet.

---

# Part A: Planner plugin

Source: [PlannerPage.tsx](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/pages/PlannerPage.tsx), `components/planner/planner-*`, `torque-task-*`, `workstream-tasks-panel.tsx`, `lib/planner.ts`, `hooks/use-planner-*.ts`. Backed by `service.PlannerService` (`internal/httpserver/planner.go`), gated by `features.planner`. A disabled feature returns `feature_disabled`, and the page shows "Turn on".

**The Planner is fully LIVE.** No GAPs block it.

### A1. Shell, tabs, counts, enable
| Need | Client method | Endpoint | Status |
|---|---|---|---|
| DB list + counts (tab badges), feature gate | `plannerDatabases()` | `GET /planner/databases` (`server.go:L213`) | LIVE |
| Turn the planner on | `setSetting('features.planner','true')` | `PUT /settings/{key}` (`L287`) | LIVE |
| Cross-DB search (≥2 chars, 250 ms debounce) | `plannerSearch(q, db?)` | `GET /planner/search` (`L214`) | LIVE |
| Live refresh on `planner.changed`, `task.created/updated/transitioned` | `useSSE` / `subscribeEvents` | `GET /events` (`L348`) | LIVE (no ids/resume. The page also polls the board every 30 s) |

### A2. Board (`/planner`, `/planner/board`) and Activity (`/planner/activity`)
| Need | Method | Endpoint | Status |
|---|---|---|---|
| Curated board / activity feed (`scope=curated\|all`, `recent_hours`, `active_hours`, `limit`) | `plannerBoard()` | `GET /planner/board` (`L215`) | LIVE |
| Task chips/titles inside board and items | `plannerTaskTitles(ids)` | `GET /planner/torque-titles?ids=` (`L216`) | LIVE |
| Open a Torque task over the board (`?open=task`) | `getTask`, `listComments` | `GET /tasks/{id}` (`L130`), `GET /tasks/{id}/comments` (`L136`) | LIVE |

### A3. Database lists (`/planner/<db>`: priorities, workstreams, roadmap, ideas, decisions, risks, inbox)
| Need | Method | Endpoint | Status |
|---|---|---|---|
| List items | `plannerList(db, {filters, sort})` | `GET /planner/{db}` (`L227`) | LIVE |
| Drag reorder | `plannerReorder(db, ids)` | `POST /planner/{db}/reorder` (`L229`) | LIVE |
| Field schema for forms | `plannerSchema(db)` | `GET /planner/schema/{db}` (`L218`) | LIVE |
| Create (board "New" creates in the inbox) | `plannerCreate`, `plannerInboxAdd` | `POST /planner/{db}` (`L228`), `POST /planner/inbox` (`L221`) | LIVE |

### A4. Detail dialog ([planner-detail-dialog.tsx](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/components/planner/planner-detail-dialog.tsx))
| Need | Method | Endpoint | Status |
|---|---|---|---|
| Get item | `plannerGet` | `GET /planner/{db}/{id}` (`L230`) | LIVE |
| Patch with `rev` (409 returns `details.current`) | `plannerUpdate` | `PATCH /planner/{db}/{id}` (`L231`) | LIVE |
| Comment / decision note | `plannerComment` | `POST /planner/{db}/{id}/comments` (`L232`) | LIVE |
| Links add/remove; cross-item relate | `plannerLinkAdd/Remove` | `POST/DELETE /planner/{db}/{id}/links` (`L233-234`), `POST/DELETE /planner/links` (`L219-220`) | LIVE |
| Decide / defer / reopen decision | `plannerDecide/Defer/Reopen` | `POST /planner/decisions/{id}/decide\|defer\|reopen` (`L224-226`) | LIVE |
| Inbox promote / dismiss | `plannerInboxPromote/Dismiss` | `POST /planner/inbox/{id}/promote\|dismiss` (`L222-223`) | LIVE |
| Workstream → tasks panel | `plannerWorkstreamTasks(id)` | `GET /planner/workstreams/{id}/tasks` (`L217`) | LIVE |
| Autolinked refs in text (`LinkText`) | see Console §C10 | | Only `torque.task` is LIVE |

Planner notes and minor gaps (not blocking):
- **Actor attribution**: `plannerActor()` hard-codes `"chrispian"` when `X-Client: browser`. It needs operator identity (see §C0.4). Severity P2.
- The workstream tag→item lookup (`ws-agent-console` → `WS-…`) does not exist. See refs §C10. Severity P2.

---

# Part B: Shared Console foundations (prerequisite for every Console area)

Source: `console/events`, `console/seams`, `console/shell`, [events/CONTRACT.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/events/CONTRACT.md), [events/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/events/BACKEND_NEEDS.md).

### C0.1 Unified event feed (`ConsoleSource`: `snapshot({since})`, `subscribe(handlers,{cursor})`)
| Need | Today | Status |
|---|---|---|
| Activity events | `ActivityStreamSource` over `/activity/agents` + `/activity/stream` (`L290-291`) | RELAY (Chimera activity-monitor :8099) |
| Torque domain events | `TorqueEventsSource` over `/events` (`L348`). The GUI synthesises ids (`torque:<type>:<id>:<now>:<n>`), so the snapshot is always empty | PARTIAL |
| One feed with stable ids, cursor, epoch, gap detail, history snapshot | none | **GAP** |

**Contract (GAP)**. Owner: **Torque** (aggregator; can wrap Chimera + own bus), aligned with the draft `event_message_stream_contract_2026_10_16`.
- `GET /api/v1/console/events?since=<cursor>&limit=` returns `{events: ConsoleEvent[], cursor, epoch}`.
- `GET /api/v1/console/stream` is SSE (named or default `message` frames). Resume uses `Last-Event-ID`. Frames: event, `gap {reason, earliest_available, high_water}`, heartbeat.
- `ConsoleEvent` envelope: `id` (globally unique, stable across redelivery), `ts`, `kind`, `source{id, machine?}`, `subjects[]{type,id,label?,url?}`, `severity` (debug..critical), `payload`, `correlationId?`, `threadId?`, `actions[]{id,label,requiresConfirm,scope[],params}`. v1.1 additive fields: `cursor`, `epoch`, `recorded_at`, `scope{task_id,project_id,worker_id}`, `corr{trace,causation,correlation,thread,idempotency_key,depth}`, `actor{kind,id,on_behalf_of,verified}`, `class{category,severity,sensitivity,visibility,labels,payload_schema}`.
- Server stamps `scope/corr/actor/class` from verified context, never from payloads. It enforces `class.visibility=operator`.
- Removal must be expressed as events (`process.exited`, agent departure). The store merges and never drops on snapshot absence.
- Kinds the GUI expects. All are currently placeholders, so the owner must define them: `agent.state`, `task.state_changed`, `message.received` / `message.*` (with `body_ref`, never body), `approval.requested{request_id,expires_at,hitl_ref,actions}` / `approval.resolved{outcome,resolver}`, `alert.raised/cleared{coalesce_key,source_event_ids,confidence}`, `pr.*`/`forge.*`, `metric.tick{metric,value,unit}`, `budget.threshold{percent,scope}`, `checkpoint.pending`, `gate.evaluated`, `phase.*`, `progress.checkpoint`, `run.state_changed`, `hitl.resolved`, `chat.message.unread`.
- History query: `GET /console/events?correlationId=` or `threadId=` for the alert "related timeline" beyond the client window.

### C0.2 StateStore (per-user item state: read/dismiss/snooze/settle/reopen)
Today: `MemoryStateStore` (in-memory, per tab). **GAP.** Owner: **Torque** (or Tangent if inbox state is centralised).
- `GET /api/v1/console/state` (optional `?prefix=`) returns `ItemState[]` `{key, status: unread|read|dismissed|snoozed|settled, snoozeUntil?, resolution?, note?, by, updatedAt}`.
- `PUT /api/v1/console/state/{key}` with body `StateOp`: `{type:'read'} | {type:'dismiss',note?} | {type:'snooze',until,note?} | {type:'settle',resolution?,note?} | {type:'reopen'}`, plus future `{type:'defer', context:{kind,ref?}, until?}`. Returns the `ItemState`.
- Push: SSE `state.changed` frames (piggyback on the console stream is fine).
- Keys are arbitrary strings: event ids, thread ids, card dedupe keys (`approval:appr-1`), `pr-inbox:<prKey>`, `pr-assign:<prKey>:<agentId>:<task>`. Requires **prefix queries**. Server-side `by` comes from auth. State must be readable by agents.
- Server-side snooze wake and defer-to-context evaluation should re-emit with a **new** event id.

### C0.3 ActionBus + IdentityConfirm (step-up)
- ActionBus is client-side dispatch. No backend of its own is needed. Each action's handler calls a domain endpoint (listed per area).
- `IdentityConfirm.confirm(ConfirmRequest{who?, what{action,title,summary?,subject?}, scope[], ttlMs?})` returns `ConfirmResult{outcome: granted|denied|cancelled|expired|unavailable, who, what, scope[], expiresAt, issuedAt, token?, reason?}`. Today only `MockIdentityConfirm` exists. **GAP.** Owner: **Auth workstream / Tether issuer (Hollis auth, PRJ-20260414-0001)**.
  - Endpoint: `POST /api/v1/auth/step-up` taking `{what{action,subject}, scope[], ttl}` and returning `{outcome, token, expiresAt, issuedAt, assurance?}` (passkey/WebAuthn UX).
  - Tokens are short-lived (2 min requested for forge comments), single-use, and bound to `who + what.action + subject + scope`. They are verified server-side by every write endpoint below. Never persist the token; audit `{outcome, scope, issuedAt, expiresAt}`.
  - Transport conventions in use: body field `confirmation` (agents), `confirmationToken` (alerts/console actions), header `X-Identity-Confirm` (forge).

### C0.4 Operator identity
`from` is hard-coded to `msg://user/local/operator` (`messaging/mock.ts` `OPERATOR_ADDRESS`), forge author is `you`, and the planner actor is `chrispian`. **GAP.** Endpoint: `GET /api/v1/me` returns `{id, label, urn, forgeLogin?}`. Owner: **Auth / Torque**.

### C0.5 Generic console action execution (optional alternative to per-domain routes)
`POST /api/v1/console/actions {kind, eventId, params, confirmationToken}` returns `{ok, message}` and emits the resulting `*.resolved` event. **GAP.** Owner: Torque.

---

# Part C: Console plugin

## C1. Agents (Fleet monitor): `/planner/agents`
Source: [fleet-agents.tsx](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/components/planner/fleet-agents.tsx), `fleet-agent-modal.tsx`, `fleet-parts.tsx`, `fleet-timeline.tsx`, [lib/fleet-agents.ts](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/lib/fleet-agents.ts), [hooks/use-fleet-agents.ts](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/hooks/use-fleet-agents.ts).

| Need | Method / endpoint | Status |
|---|---|---|
| Snapshot `{agents:[{id, provider, pid, session_id, cwd, state: waiting\|busy\|idle\|unknown, waiting_for, started_at, last_activity, confidence{}}]}` | `getActivityAgents()` → `GET /activity/agents` (`L290`) | RELAY (Chimera :8099) |
| Change stream + `gap` (resync every 20 s) | `subscribeActivity()` → `GET /activity/stream` (`L291`) | RELAY |
| Optional row fields `model`, `last_action`, `last_action_at`, `workstream_id`, `workstream_name`, `unread_messages` | same snapshot | PARTIAL. The UI degrades to null when absent. Owner: Chimera activity-monitor |
| State timeline / transitions history | client-only `trackTransitions` ("the source keeps no history") | **GAP**. Needs `GET /activity/agents/{id}/history?since=` or ≥1 h backfill of `agent.state` events in the console snapshot. Owner: Chimera (+ Torque relay) |
| Mail badge fallback `unreadCounts(addresses)` | `GET /messages/inbox` is destructive, so it is not implemented | **GAP**. See C7 #9 |
| Modal actions: Nudge / Request update / Chat | Messaging C7 / Chat C8 | PARTIAL (mail LIVE) |
| `?agent=` deep link | client only | n/a |
| Fleet-process ↔ catalog-agent join key | none | **GAP** (open question: session id / launch id / cwd+provider). Needed by C2 `live`, C8 capability |

## C1b. Agents leaderboard demo: `/planner/agents-demo` (hidden tab)
[agent-activity.tsx](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/components/planner/agent-activity.tsx) fed by `use-agent-activity.ts`, which is **100% demo seeds**. **GAP.**
Shape `AgentActivity{id, name, role, group (workstream), state, lastActionAt, lastAction, stats{done24h, failed24h, actions24h, streak, avgTaskMin}, series[24 buckets], history[{at, kind: started|done|failed|note, text}]}`.
Suggested: `GET /api/v1/agents/activity?window=24h`, derived from Torque runs (`/runs`, `/runs/timeseries` `L262-264`, which are LIVE building blocks) joined with Chimera activity. Owner: Torque. **Recommendation: drop from the port or fold into C1** until owned.

## C2. Builder: Agent directory, editor, lifecycle (`/planner/builder`)
Source: `console/agents/*` ([client.ts](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/agents/client.ts), `types.ts`, `plan.ts`, `lifecycle.ts`), [agents/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/agents/BACKEND_NEEDS.md). The default client is `createMockAgentsClient()` (`context.tsx:5`). **Everything here is GAP.** Without a backend the Builder shows nothing.

Owner: **Tether** (catalog: `tether_catalog_list_agents`, `tether_agent_list/show/create/edit`, `tether_catalog_list_providers/projects/boot_profiles`, `tether_workstream_assign`, team ops (the docs say `tether_team_*`; the MCP surface today exposes `tether_group_*`)), fronted by a **Torque** `/api/v1` facade (same auth as the rest) that also joins Fleet live state (Chimera) and Torque assignments.

| AgentsClient method | Proposed route | Response essentials | Prio |
|---|---|---|---|
| `listAgents({search,provider,role})` | `GET /agents` | `AgentRecord[]`: `identity{id,name,urn?,roles[],summary?,enabled,retired?,retiredAt?}`, `runtime{provider,model?,project?,worker?,workspaceMode?,bootProfile?,prompt?}`, `policy{permissionMode,sandbox?,network?,scopes[],rules[]}`, `tools[]` (resolved from project `mcp.servers`), `skills[]`, `teams[{teamId,teamName,role}]`, `source{path,revision?,updatedAt?}`, `live?{state: waiting\|busy\|idle\|offline, sessions, lastActivity}` | **P0** |
| `getAgent(id)` | `GET /agents/{id}` (404 = null) | `AgentRecord` | **P0** |
| `planChange({draft, baseRevision, note})` | `POST /agents/{id}/plans` | `AgentPlan{id, agentId, status: proposed\|invalid\|approved\|applied…, changes[], risks[], issues[{level,…}], risk, requiredScope[], approval?{by,at,expiresAt,scope}}`. Server recomputes diff/risk/validation | P1 |
| `getPlan(id)` | `GET /agent-plans/{id}` | `AgentPlan` | P1 |
| `approvePlan(id, confirmation)` | `POST /agent-plans/{id}/approve` `{confirmation}` | `AgentPlan` (approval time-boxed ~15 min) | P1 |
| `applyPlan(id, confirmation)` | `POST /agent-plans/{id}/apply` | `ApplyOutcome{status: applied\|failed\|conflict\|expired, agent, revisionBefore, revisionAfter, auditId}`. Atomic; `conflict` on revision mismatch | P1 |
| `rollbackApply(id, confirmation)` | `POST /agent-plans/{id}/rollback` | `ApplyOutcome{rolled_back\|conflict}` (new revision, never rewrite) | P1 |
| `listAudit(subjectId)` | `GET /agents/{id}/audit` (also accepts team ids and `"assignments"`) | `AuditEntry[]` newest first: actor (from auth), planId, summary, risk, changeCount, revisions, step-up meta | P1 |
| `planLifecycle(req)` | `POST /agent-plans/lifecycle` | `AgentPlan` + `lifecycle{request,summary,items[],impact?}`. Ops: `agent.create\|retire\|restore`, `team.create\|update\|delete`, `assignment.update`. Reuses approve/apply/rollback | P1 |
| `getRetireImpact(id)` | `GET /agents/{id}/retire-impact` | `{sessions{count,state}, tasks[], workstreams[], teams[{teamId,teamName,role,soleCoordinator}]}` from live Fleet + Torque | P1 |
| `listTemplates()` | `GET /agent-templates` | `AgentTemplate[]{id,name,summary,draft}` | P1 (CreateWizard) |
| `listTeams()` / `getTeam(id)` | `GET /teams`, `GET /teams/{id}` | `TeamRecord{id,name,summary,members[{agentId,role}],policy{permissionCeiling,scopesCeiling[],rules[],reportTo?},revision,updatedAt}`. Team policy has no Tether home yet | P1 (TeamsView empty without it) |
| `listAssignments({agentId,targetId})` | `GET /assignments?agent_id=&target_id=` | `AssignmentRecord[]{id, agentId, target{type: workstream\|task, id, label?}, role?, since}` (needs stable ids). Partial building block: `GET /workstreams/claims` (`L299`) | P1 (AssignmentsView empty) |
| Step-up for approve/apply/rollback | C0.3 | scopes `agent.write`, `agent:<id>`, `agent.write.privileged` (high risk), `policy.grant`, `agent.create\|retire\|restore`, `team.write`, `assignment.write` | P1 |

Server-enforced semantics (from BACKEND_NEEDS): `high` risk requires a note; four-eyes is an open question; retire is one atomic transaction (disable, remove from teams, release assignments, drain/stop sessions); `restartRequired` per change.

**Orphan design spike** [agent-builder.tsx](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/components/planner/agent-builder.tsx) is not imported anywhere. Its option catalogs (`ARCHETYPES`, `RULES`, `SKILLS`, `SCOPES`, `PROVIDERS`, `PROJECT_SERVERS`, `WORKERS`) are demo copies. If ported, it needs `GET /agents/catalog/options` returning `{providers[{id,models[]}], projects[{id, mcpServers[]}], workers[], skills[], rules[], scopes[], bootProfiles[], archetypes/templates[]}`. Owner: Tether (`tether_catalog_list_*`). **GAP**, P2. Prefer merging it into the C2 CreateWizard.

## C3. Events debug / Console feed
`events/debug-panel.tsx` and every consumer of `useConsoleEvents`. Status as C0.1: activity RELAY + Torque `/events` PARTIAL, unified feed GAP (P1).

## C4. Alerts (tray, toasts, badge, detail modal)
Source: `console/alerts/*`, [alerts/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/alerts/BACKEND_NEEDS.md), [alerts/detail/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/alerts/detail/BACKEND_NEEDS.md).

| Need | Status | Contract / owner |
|---|---|---|
| Alert-worthy events (mapping by kind: `approval.requested`, `message.received`, `alert.raised`, `pr.*`, `budget.threshold`, `checkpoint.*`, `fleet.node.*`) | PARTIAL. Live feed renders, but almost none of these kinds are produced today, so the tray is near-empty | Producers emit the kinds in C0.1 with `subjects[].url` or `navigate` action `params.href` (same-origin or http(s)). Owner: Torque/Chimera/Tangent |
| Item state (read/dismiss/snooze/settle across devices) | **GAP** (Memory) | C0.2 |
| `AlertDecisionClient.decide({eventId, decision: approve\|deny, note?, confirmationToken?, subject?})` returns `{ok, message?}` | **GAP** (`createMockDecisionClient`) | `POST /api/v1/approvals/{eventId\|request_id}/decide`. Verifies the token (scope `approval.approve` or the offered scope), applies via **go-hitl**, is idempotent per id, and writes the settle server-side + emits `approval.resolved`. Owner: Torque over go-hitl / Tangent |
| Reply to sender | PARTIAL | Needs `payload.fromAddress` (`msg://agent/...`) stamped by producers and slug→URN resolution (C7 #4/#10) |
| Defer-to-context | **GAP** | StateStore `defer` op + server evaluator (C0.2) |
| Related timeline | PARTIAL (client window only) | `GET /console/events?correlationId=` |
| Alert prefs (quiet, toast threshold) | GAP (localStorage) | see C14 |

## C5. Cards runtime (`console/cards`)
Rules run client-side over the feed (`deriveCards`). This is fine without a backend. Gaps from [cards/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/cards/BACKEND_NEEDS.md):
- Action handlers `approve/reject/reply/navigate/api` are mock-registered (`registerMockCardHandlers`): **GAP**. They need real endpoints accepting `ConfirmResult.token`, returning `{status,message}`, idempotent on `(card key, actionId)`. They reuse C4 decide / C6 respond / C7 send.
- Resolution events `approval.resolved`, `pr.merged` with the same `correlationId` or subject: GAP (producers).
- Optional `card.upsert` server-evaluated cards and served `CardRuleSet` JSON: GAP, P3.
- Tangent interaction-kind schema (`tangent_interaction_kind_list`) mapping to `CardKindDef`: GAP, P2. Owner: Tangent.
- Note: `CardTray/CardToaster/CardModal` surfaces are unmounted in the app today (`mountSurfaces={false}`).

## C6. HITL cards (approval, question, form, secret)
Source: `console/hitl/*` ([client.ts](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/hitl/client.ts), `types.ts`), [hitl/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/hitl/BACKEND_NEEDS.md). Only `MockHitlClient` exists, and the provider is **not mounted**. Without a client, no HITL card ever renders.

Owner: **Tangent** human queue (`tangent_hitl_get`, `tangent_interaction_get/submit`, `tangent_hitl_withdraw`, queue SSE), with **go-hitl** as the decision wire. Torque-side building blocks that are LIVE today: `GET /checkpoints/pending` (`L156`), `GET /checkpoints/{cid}` (`L157`), `POST /checkpoints/{cid}/respond|cancel` (`L158-159`), `GET /checkpoint-workflows[/{type}]` (`L153-154`). These could back `hitl.question` for task checkpoints.

| HitlClient method | Proposed route | Essentials | Prio |
|---|---|---|---|
| `list({status?,…})` | `GET /api/v1/hitl/requests?status=pending` | `HitlRequest` union by `type: approval\|question\|secret\|form`. Common: `id,title,prompt?,requester{id,label?,kind},risk: low..critical,createdAt,expiresAt?,contextLinks[ConsoleRef],status: pending\|answered\|expired\|withdrawn(+escalated),audit[{at,actor,actorKind?,action,note?}],outcome?,correlationId?,tangentKind?`. Approval: `scope[],effects?[],allowDefer?,class?,planHash?`. Question: `mode: choice\|multi\|text,options[],allowOther?`. Form: `fields[{id,label,kind: text\|textarea\|number\|select\|checkbox,required?,options?,default?}]`. Secret: `fieldLabel,purpose,destination?` | **P0** (view) |
| `get(id)` | `GET /hitl/requests/{id}` | same | **P0** |
| `subscribe(listener)` | SSE `GET /hitl/stream` (push the changed item, not a bare ping) or console events `hitl.*` | | **P0** |
| `respond(id, response, ctx)` | `POST /hitl/requests/{id}/respond` `{response: {type:'approval',decision,reason?}\|{type:'question',answer}\|{type:'form',values}, confirmationToken, idempotencyKey, planHash?}` | Verifies step-up (`hitl:approve` + request scopes), refuses after `expires_at`, refuses self-approval, returns `terminal_conflict` on a duplicate, emits `hitl.resolved`/`approval.resolved` | P1 |
| `submitSecret(id, value, ctx)` | `POST /hitl/requests/{id}/secret` (TLS same-origin) | Value goes straight to the shared secrets lib / Cerberus vault and is never stored in Tangent/journal/bus/logs. Returns status + handle only. Needs a new Tangent secret interaction kind | P2 |
| Expiry enforcement + `approval.resolved{outcome: expired}` | server | GAP in Tangent today | P1 |

## C7. Messaging (composer, quick actions, fleet actions, delivery status, recent thread, unread)
Source: `console/messaging/*`, [messaging/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/messaging/BACKEND_NEEDS.md). The live adapter `createMailMessagingClient` (`adapter.ts`) is real.

| MessagingClient method | Endpoint | Status |
|---|---|---|
| `send(SendRequest{to,from?,intent: message\|nudge\|update_request,body,subject?,threadId?,inReplyTo?,templateId?,expectedResponse?,confirmedBroadcast?})` | `POST /messages` (`L333`). Intent maps to `kind` (`notice`/`request`); console metadata goes in `metadata` | LIVE |
| `getDelivery(id)` → `queued\|delivered\|read\|replied\|failed` | `GET /messages/{id}` (`L337`) + `GET /messages/thread/{tid}` (`L336`) | PARTIAL: only `delivered_at`/`consumed_at`. Needs explicit `state`, `failed_at`+reason, server-computed `replied` |
| `recentThread({threadId})` | `GET /messages/thread/{tid}` | LIVE |
| `recentThread({with: urn})` (per-peer, both directions) | none | **GAP**: `GET /messages?peer=<urn>&before=<ts>&limit=` |
| `resolveRecipient(ref)` (slug/name → URN, kind, label, broadcast, group size) | URN parse only | **GAP**: `GET /messages/recipients/resolve?ref=`. Needs the agent registry (Tether `tether_registry_lookup`) |
| `unreadCounts(addresses)` | inbox is destructive | **GAP**: `GET /messages/unread?to=<urn>&to=<urn>` returns `{urn: n}` (non-destructive) |
| Batch delivery status | per-message polling | **GAP**: `GET /messages/status?ids=` or use `GET /messages/subscribe?to=<operator>` (`L335`, LIVE but unused) |
| Server guard rails (nudge rate limit per (operator,recipient), broadcast confirm flag, 4000-char cap), `Idempotency-Key`, metadata round-trip | none | **GAP** (Torque messaging) |
| Operator `from` | hard-coded | GAP (C0.4) |

Owner: **Torque** messaging store (go-messaging) + **Tether** registry for resolution.

## C8. Chat (dock, thread view, composer, tool chips, capability badge)
Source: `console/chat/*`, [chat/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/chat/BACKEND_NEEDS.md). Live mode = `createMailChatClient` over C7 (mail-only, polling, `agents: []` roster).

| ChatClient method | Status | Contract / owner |
|---|---|---|
| `send(threadId, body)` (mail path) | LIVE via `POST /messages` | — |
| `history(threadId,{before,limit})` → `{messages: ChatMessage[], hasMore}` | PARTIAL (no cursor) | `GET /messages?peer=&before=&limit=` (C7) |
| `listThreads()` → `ChatThread{id, agent: Recipient, title, capability{mode,sessionId?,detail?}, unread, lastAt, lastPreview?}` | **GAP** (host passes `agents: []`, so the dock has no roster) | `GET /api/v1/chat/threads`. Owner: Torque |
| `markRead(threadId)` / server unread | **GAP** (counts only while subscribed) | StateStore `read` keyed by thread id (C0.2) + push |
| `subscribeThreads(listener)` | **GAP** (no-op) | SSE thread-list changes |
| `subscribe(threadId)` live stream: `message.start{id,thread_id,from,at}`, `message.delta{id,text,seq}`, `tool.start/end{message_id,tool_id,name,status,summary(redacted)}`, `message.end{id,error?}`, `message.delivery{id,state}`, `capability` | **GAP** (mail polling only) | `GET /chat/threads/{id}/stream` (SSE, `Last-Event-ID`; stream token or same-origin injection). Owner: **Nanite chat-stream / Outpost hook sessions**, relayed by Torque |
| Capability per agent | **GAP** | `GET /agents/{id}/chat` returns `{mode: live\|mail-only\|offline, sessionId?, detail}` + push. Needs the C1 join key. Building block: `GET /sessions` (`L304`) |
| Send into a live session / cancel | **GAP** | `POST /chat/threads/{id}/messages {body, idempotency_key}` (inject via Outpost hook), `POST .../cancel` |
| `chat.message.unread{thread_id,agent,count,preview}` event for alerts | **GAP** | producer: Torque messaging |

## C9. Workflow / steering cards
Source: `console/workflow/*`, [workflow/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/workflow/BACKEND_NEEDS.md). Only `MockWorkflowClient`, and the provider is not mounted. **GAP.**
Owner: **Hadron** (workflow runs: `hadron_workflow_run_inspect`, `run_events`, `run_subscribe`, `gate_submit`, `signal`, `run_cancel`) + **Torque** (checkpoints, LIVE) + **Tether** (steer via message/session).

| WorkflowClient method | Proposed route | Essentials | Prio |
|---|---|---|---|
| `list()` / `get(id)` | `GET /api/v1/workflow-runs[/{id}]` | `WorkflowRun{id,title,state: running\|paused\|blocked\|awaiting_plan\|completed\|failed\|cancelled, priority, agent{id,label?}, phases[{id,label,status,progress?{done,total},gate?{id,label,status: open\|passed\|failed\|overridden,criteria[{label,met}],requiresHuman,evaluatedAt?},startedAt?,endedAt?}], blockedReason?, redirect?, escalatedTo?, plan?{status,items[{id,label,summary?,included}],proposedAt,note?}, checkpoint?{id,question,options[],allowText,status,answer?,emittedAt}, subjects[], audit[], createdAt, updatedAt}`. Server-side projection | **P0** (view) |
| `subscribe()` | SSE of changed runs, or console events `run.state_changed{runId,to,reason}`, `phase.*`, `gate.evaluated{…}`, `progress.checkpoint{runId,phaseId,done,total}` | | **P0** |
| `steer(runId, cmd)` (`pause\|resume\|redirect{instruction}\|escalate{to,note?}\|priority{priority}`) | `POST /workflow-runs/{id}/steer` `{cmd, confirmationToken, idempotencyKey}` (scope `workflow:steer`) | Rejects non-active runs | P1 |
| `submitGate(runId,gateId,pass\|fail\|override,note)` | `POST /workflow-runs/{id}/gates/{gateId}` → `hadron_workflow_gate_submit` (override requires a reason; scope `workflow:gate`) | | P1 |
| `respondCheckpoint(runId,cpId,answer)` | `POST /checkpoints/{cid}/respond` (`L158`) | **LIVE** (needs run↔checkpoint mapping) | P1 |
| `decidePlan(runId, approve\|reject, included[], note)` | `POST /workflow-runs/{id}/plan` (scope `workflow:plan:approve`) | No Torque/Hadron plan-proposal API exists: **GAP** | P2 |
| Flow definitions (server-stored, versioned) | `GET/PUT /console/flows` | GAP | P3 |

## C10. Autolink + Refs (chips, hovercards, drawer)
Source: `console/autolink/*`, `console/refs/*`, [refs/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/refs/BACKEND_NEEDS.md), [autolink/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/autolink/BACKEND_NEEDS.md). Interface: `KindResolver.resolveMany(canonicals) → {id: ResolveOutcome}` (batch). Chips still link without resolvers; only hovercards degrade.

| Kind | Status | Need (batch `ids → {id: {title, status, updated_at, subtitle?, description?, fields?} \| null}`) | Owner |
|---|---|---|---|
| `torque.task` | **LIVE** `GET /planner/torque-titles` (`L216`) | optional `description`, `epic_id`, owner, sprint | Torque |
| `torque.epic/project/sprint/plan` | GAP (mock) | `GET /planner/torque-briefs?ids=` keyed by prefix (+open/done counts) | Torque |
| `workstream` | GAP | batch by tag/name `{name,status,open counts,owner}` + tag→`WS-` id route | Torque planner |
| `agent` | GAP | batch by id/name `{name,provider,state,cwd,last_activity,current_task}` (registry + fleet) | Tether + Chimera via Torque |
| `tesseract.item` / `tesseract.key` | GAP | batch by ULID or `[domain@]ns#key` `{title,domain,namespace,key,updated_at}` + a GUI-reachable read path | Tesseract (via Torque relay) |
| `forge.pr/commit/repo` | GAP (ForgeClient mock) | see C11; plus `getCommit`, batch `getPrs(refs[])`, watched repo list, host registry | Torque forge → Cerberus |
| `url` | none | server-side unfurl (SSRF-safe) | Torque, P3 |
| `path` | none | optional existence check | P3 |
| `forbidden` outcome | contract | 403 vs 404 distinction | all |
| Per-user autolink opt-out | localStorage | settings endpoint (C14) | P3 |

## C11. Forge: PR inbox (`/prs/inbox`), PR workspace, forge refs
Source: `console/forge/*`, [`plugins/console/forge.go`](file:///home/chrispian/dev/hollis-labs/apps/tachyon/plugins/console/forge.go).
**Status: LIVE in Tachyon (no Torque dependency).** Tachyon's `console` plugin subprocess serves `/api/plugins/console/v1/forge/*` directly against GitHub (`https://api.github.com`) using `GITHUB_TOKEN` / `GH_TOKEN` / `gh auth token` and `GITHUB_ORG` (default `hollis-labs`).

| ForgeClient method | Route (base `/api/plugins/console/v1/forge`, `P=/repos/{host}/{owner}/{name}/pulls/{n}`) | Status in Tachyon |
|---|---|---|
| `listPendingPrs()` | `GET /pulls/pending` → `{items: PendingPrSummary[]}` with `reasons` (`review_requested, checks_failed, changes_requested, conflicting, unassigned, stale`), `checksSummary`, `reviewSummary`; 30s cache | **LIVE** (`plugins/console/forge.go`) |
| `getPr(ref)` | `GET P` → `PullRequest` incl. `checks` (per head sha), `reviews`, `mergeable`, `headSha`, `taskId?` | **LIVE** (`plugins/console/forge.go`) |
| `getDiff(ref)` | `GET P/diff` → `{items: FileDiff[]}` (unified diff parsed into hunks and numbered `DiffLine`s, `truncated`, `binary`) | **LIVE** (`plugins/console/forge.go`) |
| `listCommits(ref)` | `GET P/commits` | **LIVE** (`plugins/console/forge.go`) |
| `listComments(ref)` | `GET P/comments` (issue + review comments merged chronologically, `inReplyTo`, `commitSha`, `reviewId`) | **LIVE** (`plugins/console/forge.go`) |
| `listPrs(q)` | `GET /repos/…/pulls` or `GET /pulls` (`state, author, assignee, q, limit`) | **LIVE** (`plugins/console/forge.go`) |
| `getRepo(repo)` | `GET /repos/{host}/{owner}/{name}` | **LIVE** (`plugins/console/forge.go`) |
| `addComment` | `POST P/comments` (conversation or inline diff comment) | **LIVE** (`plugins/console/forge.go`) |
| `reviewPr` | `POST P/reviews` (`approve`, `request_changes`, `comment` + inline comments) | **LIVE** (`plugins/console/forge.go`) |
| `assign` | `PUT P/assignees` | **LIVE** (`plugins/console/forge.go`) |
| Inbox triage state | StateStore keys `pr-inbox:*`, `pr-assign:*` with prefix query | C0.2 (in-memory fallback active; server persistence GAP) |

## C12. Metrics (scope panels on Epic/Sprint pages, fleet charts, metric cards)
Source: `console/metrics/*`, [metrics/BACKEND_NEEDS.md](file:///home/chrispian/dev/hollis-labs/apps/torque/apps/gui/src/console/metrics/BACKEND_NEEDS.md). Interface `MetricsSource.load(scope) → ProjectMetrics{progress[{status,count}], burn{startTs,endTs,total,completed[{t,v}],scopeChanges?[{t,label,delta}]}, tiles[{id,label,value,unit?,previous?,series?}], breakdown[], burnQuality, notes[]}`. `ScopeMetricsPanel` is mounted on `EpicDetailPage` and `SprintDetailPage` via `TorqueMetricsSource`.

| Need | Endpoint | Status |
|---|---|---|
| Status counts per scope (`epic:/sprint:/project:`) | `GET /tasks/rollup` (`L128`) | LIVE |
| Task list for derived burn | `GET /tasks?fields=summary` (`L124`, paged, capped 1000) | LIVE |
| Scope dates | `GET /sprints/{id}` (`L186`), `GET /epics/{id}` (`L240`) | LIVE |
| Completion time | burn uses `updated_at` | PARTIAL. Needs `completed_at`/`status_changed_at` on task summaries + a transition log |
| Scope membership history | inferred from `created_at` | **GAP**: `{task_id,scope_id,action,ts}` log or daily `{t,total,done}` snapshot per scope (`GET /metrics/burn?scope=`) |
| Tag scope rollup | pages the task list | **GAP**: `GET /tasks/rollup?group_by=tag` |
| Run tiles by scope (failed runs, tokens, cost) | `/runs` has no scope filter (`L262`) | **GAP**: scope filter on `/runs` + `/runs/timeseries` |
| Fleet metrics (working/idle/waiting over time, tokens/min, cost) | folds `agent.state` + `metric.tick` from the console store | **GAP**: `metric.tick{tokens_per_min,cost(usd)}` events, agent departure events, ≥1 h snapshot backfill or a `fleet.counts` series. Owner: Chimera + Torque |
| Metric card kinds `metric.progress/burndown/fleet` | placeholders | GAP (adopt or remap), P3 |

Owner: **Torque** (task/runs data); Chimera for fleet ticks.

## C13. Rules (alert rule packs)
`rules/default-pack.ts` is a client-side default (data, not a mock). This works without a backend.
- `GET /api/v1/console/rules` returns the effective `AlertRulePack` (v1 JSON): default + agent contributions + per-user disables (`enabled:false`, stable rule ids). **GAP**, P3.
- Server escalation and coalescing (`alert.raised` with `count` + stable id per key): GAP, P3.
- Producers of `budget.threshold{percent,scope}` and `checkpoint.pending` (navigable subject): GAP, P2 (Torque).

## C14. Prefs (Console/alert preferences)
`ConsolePrefsStore` is localStorage (`console.alerts.prefs.v1`): `quiet, toastMinSeverity, quietHoursEnabled/Start/End (+IANA tz), mutedSources[], coalesceWindowSec`. **GAP** (server-backed), P3.
Proposed (kit-settings host contract): `GET /admin/settings/console.alerts`, `POST …/validate`, `POST …/update {set, unset}`, `POST …/reset`, with ETag/If-Match and push. Alternatively store under the existing `GET/PUT /settings/{key}` (`L286-287`, LIVE but global, not per-user). Owner: Torque. Rule: critical alerts always toast.

## C15. Seams / Shell
- `seams/action-bus.ts`: client-only. No backend needed.
- `seams/state-store.ts`: C0.2 (GAP).
- `seams/identity-confirm.ts`: C0.3 (GAP).
- `shell/*` (ConsoleIntegration, chat dock, enabled flag): wiring only. In Tachyon, mount the HITL and Workflow providers with live clients (today they are deliberately unmounted).

---

# Part D: Prioritized gap list

**P0: blocks a core view from showing anything (once mocks are removed)**
1. **Builder agent directory**: `GET /api/v1/agents`, `GET /api/v1/agents/{id}` (`AgentRecord`). Tether catalog via Torque facade, with live join from Chimera. (C2)
2. **Forge PR inbox**: `GET /api/v1/forge/pulls/pending`, with Torque watched-repo aggregation over the new Cerberus `list_pull_requests`. (C11)
3. **Forge PR workspace**: `GET …/pulls/{n}` (with checks/reviews/mergeable) and `GET …/pulls/{n}/diff`. Cerberus `get_pull_request`, `list_check_runs`, `get_pull_request_diff`. (C11)
4. **HITL cards**: `HitlClient` list/get/subscribe (`GET /api/v1/hitl/requests`, `GET /hitl/requests/{id}`, push stream). Tangent queue + go-hitl; Torque `/checkpoints` can seed `question`. (C6)
5. **Workflow cards**: `WorkflowClient` list/get/subscribe (`GET /api/v1/workflow-runs`, run projection + push). Hadron. (C9)

**P1: view renders but is empty, wrong, or read-only, or all writes are blocked**
6. IdentityConfirm step-up issuer + server verification (C0.3). This gates every write: agent plans, approvals, HITL, forge, steering.
7. Unified console feed with ids/cursor/epoch/gap + producers of `approval.requested/resolved`, `alert.raised/cleared`, `hitl.resolved`, `run.state_changed` (C0.1). Without it the alert tray stays near-empty.
8. Server StateStore `GET/PUT /console/state` + push + prefix queries (C0.2).
9. Approval decide endpoint (`POST /approvals/{id}/decide`, go-hitl, idempotent, server-side settle) (C4/C5).
10. Builder writes and sub-views: agent plans (plan/get/approve/apply/rollback/audit), lifecycle plans, retire-impact, templates, teams, assignments (C2).
11. HITL `respond` with expiry enforcement and self-approval refusal (C6). Workflow steer/gate (C9).
12. Messaging: non-destructive `GET /messages/unread`, slug→URN `GET /messages/recipients/resolve`, per-peer `GET /messages?peer=`, explicit delivery `state`, `Idempotency-Key` (C7). Operator identity `GET /me` (C0.4).
13. Chat: `GET /chat/threads` roster + server unread (C8). Fleet↔catalog join key (C1).
14. Forge: commits, comments, listPrs, addComment/reviewPr/assign with `X-Identity-Confirm`. Inbox triage state (C11).

**P2: degraded fidelity**
15. Chat live session stream + capability + live send (Nanite/Outpost) (C8).
16. Fleet history backfill / `agent.state` history; activity-monitor optional fields (`model`, `last_action`, `workstream_*`, `unread_messages`) (C1).
17. Ref resolvers for epic/project/sprint/plan, workstream, agent, tesseract; forge `getCommit`, batch PR summaries, watched repo list (C10/C11).
18. Metrics: `completed_at`, scope-membership log/burn series, `group_by=tag`, run scope filter, fleet `metric.tick` (C12).
19. HITL secrets via the shared secrets lib + Tangent secret kind; HITL `class`/`planHash`/`escalated` (C6).
20. Workflow plan proposals (`decidePlan`) (C9). Forge merge/resolveThread/lazy diff/live PR events (C11).
21. Planner actor from auth instead of the hard-coded `chrispian`. Workstream tag→id lookup (A4).
22. Agent builder option catalogs (if the spike is kept) (C2). Leaderboard demo data (C1b): recommend dropping.

**P3: nice to have**
23. Server rule packs, escalation, coalescing (C13). Server-backed console prefs (C14). Autolink opt-out pref.
24. Server-evaluated `card.upsert`, served card rule sets, Tangent kind mapping (C5). Flow definitions (C9).
25. URL unfurl, path existence (C10). Metric card kind names (C12).

### Ownership summary
| Service | Gaps |
|---|---|
| **Torque** | console feed/stream, StateStore, console actions, approvals decide (over go-hitl), `/me`, messaging extensions, chat thread list, agents/teams/assignments facade, forge facade + pending aggregation, ref brief endpoints, metrics (completed_at, burn, tag rollup, run scope), console prefs/rules |
| **Tether** | agent catalog + edits, teams (team policy field), assignments, registry slug→URN, step-up issuer (with auth workstream) |
| **Chimera (activity-monitor :8099)** | fleet optional fields, history/backfill, departure events, `metric.tick` tokens/cost, epoch + gap detail |
| **Tangent** | HITL queue read/respond/subscribe with content, expiry enforcement, secret kind, interaction-kind schema |
| **Hadron** | workflow run projection, events, gate submit, signals (steer) |
| **Cerberus github plugin** | all PR ops (`list/get_pull_request`, `list_check_runs`, diff, commits, comments, review, assignees, `get_commit`), `rate_limited` code |
| **Nanite / Outpost** | live chat session stream + inject |
| **Tesseract** | batch item/key brief read path |
| **Auth workstream** | IdentityConfirm step-up tokens, operator identity, assurance levels |
