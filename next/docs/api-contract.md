# Tachyon API contract (Substrate port)

The agreement between the backend slices (B*) and the GUI slices (G*) of the port. Port plan:
`docs/substrate-port-plan.md`. Source of the route list: the GUI inventory of 2026-11-14, section 12
(229 SPA call rows, 215 backend routes). Where this file and a slice disagree, the slice changes this file in
the same PR.

Status: **B1.** The rules below are in force from B1. A route group is served once the slice that ports it lands.
Until then the guard refuses it. Nothing in this file is implemented by B1 except sections 1–3, the plugin
allowlist mechanism of section 6, and the route-scope check.

## 1. Paths

- **Unchanged.** Every path the SPA calls today keeps its method and path. That includes the
  `/api/plugins/console/v1/...` and `/api/plugins/{id}/stream/{name}` prefixes; the planner client stays on
  `/api/plugins/console/v1/planner`. A slice that changes a path says so here.
- **Removed:**

  | Path(s) | Why |
  |---|---|
  | `GET /api/plugins` (the plugin list) | Only `PluginGate` read it; the nav now comes from the manifest. |
  | `GET /api/health` | Never called. Health is `/healthz`. |
  | `/plugins/*` static plugin assets | The plugin UIs compile into one SPA. |
  | `approval-inbox/session`, `/status`, `/elevate`, `/de-elevate`; `settings/elevation-ttl` | Step-up elevation is gone. |
  | `/api/plugins/console/stream/messages` | Unused. |
  | `auth/step-up` (Torque passthrough) | Old identity-confirm seam. |
  | `approval-inbox-seed`, `/api/cache`, the card seed | Dev and stub routes, not mounted. |
  | `/api/approvals/trusted/v1/*` | Basalisk WebAuthn proofs; strict mode is not ported. |

- **Framework routes the SPA uses** (Substrate identity module, through `kit-identity`):
  - Public (no credentials):
    - `GET /api/setup` answers `setup_required` and `setup_over_http`.
    - `POST /api/setup` always answers 403 `setup_offline`.
    - `POST /api/login`
  - Need a session:
    - `POST /api/logout`
    - `PUT /api/password`
    - `GET|PUT /api/identity/profile`
    - `GET /api/identity/sessions` and `DELETE /api/identity/sessions/{id}`
    - `/api/identity/keys` and the users pages: these need identity's admin scope, `tachyon.admin`, which the owner
    holds through `*`.
  - Health: `GET /healthz` (`{status, checks}`).

## 2. Requests

- **Same origin, cookie session.** The identity module's session cookie is turned into the bearer the guard
  verifies. The old `tachyon_approvals` cookie and the Tangent bridge are gone.
- **`X-Requested-With` on every write.** POST, PUT, PATCH and DELETE from the browser carry
  `X-Requested-With: tachyon-ui` and a same-origin `Origin`.
  - The identity middleware refuses a cookie write without them (403).
  - The SPA sends every write through one fetch wrapper. That includes the agents-monitor raw fetches,
    AvatarStudio and the app-agent stream.
  - `navigator.sendBeacon` cannot set headers, so it becomes `fetch(..., {keepalive: true})`.
- **Machine callers** send `Authorization: Bearer <identity API key or minted token>`. Routes that answer only
  browsers (approvals inbox, transcripts, alerts, terminal, action runs, settings registry) keep refusing a bearer
  header, as today.
- **Host.** Only loopback names and the operator's `tachyon.allowed_hosts` (behind a local TLS proxy carrying
  `X-Forwarded-Proto: https`) are served. Any other `Host` gets 403.

## 3. Errors

Every 4xx/5xx from Tachyon's own API is the flat body:

```json
{ "code": "conflict", "message": "the item changed", "details": { "current": { } } }
```

- `code` and `message` are always present; `details` is optional.
- The framework flattens `{"error": {"code", "message"}}` on the way out, so the guard's own refusals use the same
  shape. `RawPrefixes` is not used.
- **The GUI parses with one parser** (inventory 12.4 lists the sites to fold into it).

### 3.1 Status codes the UI acts on

| Status | Code | Meaning | UI |
|---|---|---|---|
| 401 | `unauthorized` | Not signed in, or the session expired. | Hand to kit-identity (sign-in screen). |
| 403 | `scope_required` | Signed in, but the caller lacks the route's scope. The guard answers it; the message is `required scope: <scope>`. A handler that answers it itself also sets `details.scope`. | Show "not allowed"; hide the control when the profile lacks the scope. |
| 403 | `operator_required` | The action needs a signed-in person holding `tachyon.operator` (section 5), and the caller is not one: a key, or a person without the scope. Answered by the handler (B3 onward). | Same as above. Operator-only controls hide from the profile scopes. |
| 403 | `route_not_mapped` | An `/api/` path no route declares (guard). | A bug in the SPA or a route not ported yet. |
| 403 | `setup_offline` | `POST /api/setup`. | No-owner screen (kit-identity). |
| 403 | `csrf` | A cookie write without `X-Requested-With` or a same-origin `Origin` (identity middleware). | A bug: the shared wrapper sends both. |
| 404 | `not_found` | No such item, or any `/api/plugins/...` path not on the allowlist (section 6). | |
| 429 | (identity) | Login rate limit; `Retry-After` is set. | kit-identity shows it. |
| 503 | `auth_unavailable` | The verifier is unavailable (guard). | Retry. |

`unauthorized`, `scope_required`, `route_not_mapped` and `auth_unavailable` come from the guard (libs
`security/auth`), checked against B1's real binary. `operator_required` is Tachyon's (B3).

### 3.2 Domain codes kept (same status where noted)

| Area | Codes |
|---|---|
| Planner | `conflict` (409, `details.current`); `invalid` (`details.errors[{path, message}]`); `cycle`; `feature_disabled`; `origin_denied`. 404/405 means the feature is absent. |
| Approvals | `operator_access_required`, which becomes `operator_required` (B3 maps it, and the UI accepts both until G-approvals); `assurance_insufficient`; `secrets_unavailable`; `item_not_pending`; `provider_handoff_required`; `confirmation_required` (422). |
| Transcripts | `configuration`, `not_found`. `session_required` goes away with the bridge; a missing session is 401. |
| Builder | `change_open`, `rate_limited`, `ack_required` |
| Registry | `tether_write_denied` |
| Agents monitor | `interrupt_unsupported`, `stale_session` |
| Files | `caster_unavailable`, `namespace_not_allowed`, `changed` |
| Workflow / HITL (Torque passthrough) | `not_active`, `expired`, `not_pending` (Torque's codes, relayed flat) |
| Alerts | `version_conflict` (`details.current`) |
| Settings | 409 / 412 / 428 on `If-Match` (ETag kept) |
| Chat upload | 413, 415 |
| App agent | `not_configured` |
| Removed with elevation | `operator_session_required` |

## 4. Responses kept

- Freshness headers `X-Tachyon-Cache` and the other `X-Tachyon-*` headers are kept. Approvals, chat and
  observations reject `stale`, `cached` or `refreshing` when they ask for fresh data, and DataStatus reads them.
- ETag / `If-Match` on settings.
- **SSE:**
  - `stream/events`, `stream/activity`, `planner/events`, approvals `/events`, builder and agents `/stream`, alerts
    (`Last-Event-ID`), and app-agent (fetch framing).
  - Each stream ends with a final event when the server stops (`httphost.Stopping`) and sends keepalives.
  - Event ids continue from the migrated data (the migration copies ids and sequences verbatim), so a client's
    `Last-Event-ID` stays valid across the cutover.
- **WebSocket:** the terminal and the action-run PTY.
  - The client connects to the `ws_path` a POST returns.
  - It authenticates with the same-origin session cookie.
  - The scope is checked at upgrade.

## 5. Scopes

**Convention.** A route scope is `tachyon.<area>.<verb>`, with the verb one of read, write, resolve, control, send
or respond. `authenticated` means any verified caller, which the handler narrows (for example, to the caller's own
account).

- An app route is never public: the only public routes are the framework's (`/healthz`, setup, login, JWKS).
- `internal/routes` refuses any other name, and `cmd/tachyon/boot_test.go` checks that every served route has its
  scope in the guard's table.

**`tachyon.operator` is not a route scope.** It is the assurance check a handler makes where elevation is required
today. `operator` holds only for a principal of kind **user** that holds `tachyon.operator`; the owner holds it
through `*`.

- An API key, or a token minted from a key, is never `operator`, whatever its scopes.
- The `access` command refuses to grant `tachyon.operator`.
- The SPA reads the scopes from `GET /api/identity/profile` to show or hide operator-only controls.

**Agent data verbs** (`/api/agent/v1/data/*`, `/api/agent/v1/mcp`, and the console `data/providers/*/verbs/*`)
keep their per-verb names (plan section 4). They are checked in the data handler against the credential's verb
scope and kind list, not in the route table, so the two naming sets cannot collide.

| Route group (under `/api/plugins/console/v1/` unless noted) | Read | Write | Operator check (in handler) |
|---|---|---|---|
| `planner/*`, `planner/events` | `tachyon.planner.read` | `tachyon.planner.write` | writes |
| `approval-inbox` items, history, audit, events | `tachyon.approvals.read` | | |
| `approval-inbox` present, defer, resolve | | `tachyon.approvals.resolve` | medium, high, destructive or cerberus resolve; batch; secrets. Unknown actions fail closed. The confirm phrase is unchanged. |
| `action-runs/*` (incl. WebSocket) | `tachyon.approvals.read` | | all, reads too |
| `chat/*`, `chat-turns/*`, `app-agent/*` | `tachyon.chat.read` | `tachyon.chat.write` | writes, and workspace file content GET. `GET threads/{ref}/continue` writes, so it takes the write scope. |
| `terminal/*` (incl. WebSocket upgrade) | `tachyon.terminal.control` | `tachyon.terminal.control` | all |
| `forge/*`, `console/state`, `console/bulk` | `tachyon.forge.read` | `tachyon.forge.write` | merge, reviews, assign, comments, assignees, audit read |
| `builder/*`, `registry/*` | `tachyon.builder.read` | `tachyon.builder.write` | writes |
| `agents/*`, `avatars/*` | `tachyon.agents.read` | `tachyon.agents.control` | control |
| `tether/*`, `objects`, `preview/*` | `tachyon.objects.read` | `tachyon.objects.write` | writes; the plan and agent reads and preview that need elevation today (owner question: is the preview elevation accidental?) |
| `catalog/*`, `tesseract/*`, `activity/*` | `tachyon.catalog.read` | | |
| `files/*` | `tachyon.files.read` | `tachyon.files.write` | all except frame documents |
| `alerts/*`, `alerts/prefs`, `console/state/al_*` and the alerts SSE | `tachyon.alerts.read` | `tachyon.alerts.write` | (tightened: Origin only today) |
| `transcripts/*` | `tachyon.transcripts.read` | | |
| `account/*` | `authenticated` (self) | `authenticated` (self) | |
| `admin/manifest`, `admin/settings/*`, `settings/*` | `tachyon.settings.read` | `tachyon.settings.write` | operator-only fields |
| `cerberus/*` | `tachyon.cerberus.read` | `tachyon.cerberus.control` | lifecycle POSTs: always (owner to confirm; today only with `TACHYON_CERBERUS_ELEVATION=required`) |
| `access/*` | `tachyon.settings.read` | | the refresh writes upstream, so it becomes `POST access/apps/refresh` with the operator check |
| `data/providers/*/verbs/*` | `authenticated` at the route | | per-verb scope plus kind list in the handler; mutating verbs need the operator check |
| `/api/plugins/console/stream/{events,activity}` | `tachyon.events.read` | | |
| Torque passthrough (section 6) | see section 6 | | |
| `/api/agent/v1/*` (MCP and data) | `authenticated` at the route | | key-to-service-identity mapping plus per-verb scope (plan section 4) |
| `/api/approvals/v1/*` (producers) | | | Tether workload JWT, off by default (answers 503 unconfigured, as today) |

## 5a. The agent data API (slice B3)

**Who may call it.** `/api/agent/v1/data/*` (and, when ported, `/api/agent/v1/mcp`) answers only identity API keys,
and tokens minted from them, that map to an active service identity (`internal/access`):

| Status | Code | When |
|---|---|---|
| 403 | `agent_required` | The caller is not an API key: a signed-in person, the owner included. |
| 403 | `credential_not_mapped` | A key that acts for no service identity, whatever its scopes (`*` included). |
| 403 | `credential_inactive` | The credential is revoked or expired, or its service identity is disabled. |

**Grants.** A credential's grant is the old token's `{verbs, kinds}`, unchanged:
- The key holds the scope `tachyon.<verb>` for each verb, for example `tachyon.planner.list`. A verb scope is never
  a route scope.
- A verb is allowed when the key holds its scope **and** the mapping grants the verb.
- A call on an object of a kind is also allowed only when the mapping grants that kind.

**Discovery.** `GET /api/agent/v1/data/providers` answers `{version: 1, providers: [{id: "tachyon", verbs:
[{name, mutates, description, input_schema}]}]}`, listing exactly the verbs this build serves that the caller may
use. A verb the caller may not use is unknown to it (404), as in the old build.

**Calls.** `GET` (reads, `?input=<json>`) or `POST` (mutations) on
`/api/agent/v1/data/providers/tachyon/verbs/{verb}`. Served from B4: `objects.list` (it names a kind, which must be
granted), `objects.get` and `objects.history` (the record's kind must be granted), and `objects.lookup_source`
(its kind must be granted).

**The console data API**, `/api/plugins/console/v1/data/providers[/tachyon/verbs/{verb}]`, answers signed-in
people only; a key is refused there whatever its scopes. The four object reads need `tachyon.objects.read`.
`GET /api/plugins/console/v1/objects/sync/status` (`tachyon.objects.read`) is the old sync status.

**Chat verbs (slice B6d).** Served on the agent API with the old names, inputs and results, over the chat
services of B6/B6b:

| Family | Verbs |
|---|---|
| Workspace | `chat.workspace.list`, `chat.workspace.get` (shared items only; text up to 32 KiB) |
| Topics | `chat.topic.add`, `chat.topic.list`, `chat.bookmark.list`, `chat.approvals.list` |
| Plans | `chat.plan.get`, `chat.plan.list`, `chat.plan.create`, `chat.plan.write`, `chat.plan.step`, `chat.plan.move_step` |
| Activity | `chat.activity.append` (types `task.closed` and `note` only), `chat.panel.open` |

- The thread rule is the old one. A direct caller names an app thread (`app:<id>`); the plan verbs also accept a
  Tether thread the caller owns (bound with `tachyon access bind`, or named after the thread's agent).
- The pinned Tether proxy acts for the session in `X-Tether-Session-Id` and only that session's own thread;
  any other thread is `403`. From any other caller the header is ignored. `X-Tether-Continues-Session-Id` and
  `X-Tether-Logical-Agent-Id` carry a plan over to a relaunched session, from the proxy only.
- `chat.approvals.list` reads the open approval cards from the migrated `chat_card_refs` and `approval_items`.
- Every verb names no object kind. The console data API does not serve them, as before.

## 5b. Planner (slice B5)

The old paths, served by Tachyon:

| Path | Read scope | Write scope | Check in handler |
|---|---|---|---|
| `/api/plugins/console/v1/planner/...` | `tachyon.planner.read` | `tachyon.planner.write` | writes need the operator check |
| `/api/plugins/planner/v1/planner/...` (legacy) | `tachyon.planner.read` | `tachyon.planner.write` | writes need the operator check |
| `GET /api/plugins/planner/stream/events` (SSE) | `tachyon.planner.read` | | |
| `GET .../tasks/{task}[/comments]` | `tachyon.planner.read` | | |
| Assignment routes (`/assignments`, `/agents/{id}/work`) | `tachyon.planner.read` | `tachyon.planner.write` | writes need the operator check |

- The planner stream ends with the server (`httphost.Stopping`). Clients reconnect with `Last-Event-ID`.
- The planner data verbs (`planner.list`, `planner.portfolio.*`) are on the agent and console data APIs:
  - agents: each portfolio database's kind must be granted, and `planner.list` without a kind needs every kind;
  - console: the person needs `tachyon.planner.read` or `tachyon.planner.write`.

## 5c. Chat (slices B6, B6b)

The old paths under `/api/plugins/console/v1/chat`, `.../chat-turns` and `.../app-agent`, served by Tachyon:

| Path (under `.../chat` unless shown) | Read scope | Write scope | Check in handler |
|---|---|---|---|
| `threads` (list, create), `threads/{ref}` (get, patch), `/messages` (list, send), `/read`, `/stop` | `tachyon.chat.read` | `tachyon.chat.write` | writes need the operator check |
| `GET events` (every thread) and `GET threads/{ref}/events` (SSE) | `tachyon.chat.read` | | |
| `GET agents` (the Tether chat roster, passed through as Tether answers; the query is dropped; `503 configuration` without Tether) | `tachyon.chat.read` | | |
| `GET outbox?launch_id=` (messages sent to a launch: bare array, newest first, at most 500) | `tachyon.chat.read` | | |
| `POST agents/{launch}/send` (`{text, idempotency_key}`; B6c) | | `tachyon.chat.write` | yes |
| `.../chat-turns/inbox`, `.../chat-turns/items/{id}/reply` | `tachyon.chat.read` | `tachyon.chat.write` | the reply |
| `.../app-agent/threads`, `/threads/{id}/messages` (+ send), `/read`, `/events` (SSE) | `tachyon.chat.read` | `tachyon.chat.write` | writes |
| `threads/{ref}/topics`, `/bookmarks` (+ `/{id}` delete) | `tachyon.chat.read` | `tachyon.chat.write` | writes need the operator check |
| `threads/{ref}/plan` (single), `/plan/steps`, `/plan/reorder` | `tachyon.chat.read` | `tachyon.chat.write` | writes |
| `threads/{ref}/plans` (several; #721/#725), `/plans/reorder`, `/plans/{id}`, steps, move | `tachyon.chat.read` | `tachyon.chat.write` | writes |
| `threads/{ref}/continue` | | `tachyon.chat.write` (GET too: it records lineage) | GET and PUT |
| `threads/{ref}/workspace` (+ `/{id}`), `/scratchpad`, `settings` | `tachyon.chat.read` | `tachyon.chat.write` | writes |
| `GET threads/{ref}/workspace/{id}/content` | | `tachyon.chat.write` | yes (file bytes) |
| `threads/{ref}/activity`, `/activity/comments`, `/context` | `tachyon.chat.read` | `tachyon.chat.write` | writes |
| `GET threads/{ref}/work` | `tachyon.chat.read` | | |

- Request and response shapes, status codes and error codes are the old build's. The old chat errors were
  already the flat `{code, message}`.
- Unserved until their slice: chat cards and their thread approval rules (`threads/{ref}/approval-rules`, with the
  chat cards slice).
- Sending to an agent is the old outbox: the send is recorded `sending`, forwarded to Tether's
  `/chat/agents/{launch}/send` with the caller's idempotency key, and its result recorded. A repeated key answers
  `{session_id, replayed: true}` once sent; the same key with other text is `409 conflict`; an unconfirmed send
  older than a day is `409 delivery_unknown`. Tether's status and body are passed back. With
  `tachyon.features.chat_sync` off it is `503 disabled` and nothing is recorded or sent.
- A thread exists when the threads aggregator lists it, as before: the Tether chat bridge's turns, the app agent's
  threads, and sessions known from plan lineage. An unknown thread is `404 not_found` "No such thread.".
- The streams are unnamed `data:` frames `{type, thread_ref, at, ...}`; `id:` is on the hub frames of the
  per-thread stream, replayable with `Last-Event-ID` (last 500 per thread). Both end when the server shuts down;
  clients reconnect.
- With the app agent not configured its routes answer `503 not_configured`; with Tether not configured, send,
  reply and stop on Tether threads answer with the configuration error, and nothing is dialled.

## 5d. Approvals (slice B7)

Served under `/api/plugins/console/v1/approval-inbox`, from the migrated `approval_*` tables:

| Route | Scope | Operator check (in handler) |
|---|---|---|
| `GET inbox`, `GET history`, `GET events`, `GET items/{id}`, `GET items/{id}/audit` | `tachyon.approvals.read` | |
| `POST items/{id}/present` | `tachyon.approvals.resolve` | |
| `POST items/{id}/resolve` | `tachyon.approvals.resolve` | medium, high, destructive (the old assurance table) |
| `POST items/{id}/defer`, `DELETE items/{id}/defer` | `tachyon.approvals.resolve` | |
| `POST batch/resolve` | `tachyon.approvals.resolve` | yes |
| `POST secrets` | `tachyon.approvals.resolve` | yes |

- Only a signed-in person uses the inbox. No principal is `401 session_required`; a key, whatever its scopes, is
  `403 requester_cannot_approve` on every route, reads included, as a bearer requester was in the old build. Below
  the action's level it is `403 operator_required`.
- Request and response shapes are the old build's. The hitl errors (stale revision, idempotency and terminal
  conflicts) are flattened to `{code, message, details}`: `details` carries the old body's other fields
  (`contract_version`, `operation`, `item_id`, `revision_kind`, `expected_revision`, `actual_revision`,
  `current_state`, `terminal_outcome`).
- Secret values are never in a list, detail, history or audit body. Without `tachyon.files.secret_key`, `POST
  secrets` answers `503 secrets_unavailable` and the rest of the inbox serves.
- Not yet joined into item bodies: `action_run` and `run_state` (action runs) and `card_receipt` (card delivery).
  They come back when those slices set the inbox's hooks.
- Not served: the producer API `/api/approvals/v1` (not ported), the trusted Basalisk API, and the Tangent
  session routes (section 1).

## 5e. Agents, ceremony and Needs you (slices B8a, B8b)

Served under `/api/plugins/console/v1`, from the migrated `agents_*`, `bus_events`, `seat_status`, `ceremony_daily`
and `checkpoint_events` tables:

| Route | Scope | Operator check (in handler) |
|---|---|---|
| `GET agents/sessions`, `agents/sessions/{id}`, `…/events`, `…/context-series`, `…/tokens`, `…/history` | `tachyon.agents.read` | |
| `GET agents/stream` (SSE), `agents/settings`, `agents/seats`, `agents/controls/{request_id}`, `agents/collector`, `agents/bus/events` | `tachyon.agents.read` | |
| `GET ceremony` | `tachyon.agents.read` | |
| `GET needs-you` | `tachyon.agents.read` | |
| `PUT agents/settings`, `POST agents/sessions/{id}/nudge`, `…/restart`, `POST agents/controls/{request_id}/skip` | `tachyon.agents.control` | yes |
| `GET tether/workers`, `tether/sessions`, `tether/sessions/{id}`, `GET catalog/launches` | `tachyon.agents.read` | |
| `POST tether/sessions`, `POST tether/sessions/{id}/launch` | `tachyon.agents.control` | yes |

- Request and response shapes, status codes and error codes are the old build's (`internal/agentsapi/openapi.yaml`).
- With `tachyon.features.agents_collector` off (the default) the collector is not started: `agents/collector`
  reports it `off`, and the data is what was migrated.
- With `tachyon.features.agents_controls` off (the default), nudge, interrupt and restart are unavailable (`409
  nudge_unsupported`, `interrupt_unsupported`, `restart_unsupported`) and the Tether launches answer `503
  disabled`; nothing is sent.
- The Tether reads and launches answer `503 configuration` without `tachyon.upstreams.tether.url`; a malformed id
  is `404 not_found`; the reviewed body fields are checked before anything is forwarded (`400 invalid_request`).
- Needs you reads the operator's mailbox from a configured Tether, and the ceremony meter reads a configured
  Torque, on demand, as before. Without them those sources report `not configured`.
- Needs you reads need no operator, as in the old build (its guard let GETs through).
- Not yet: the ceremony meter's weekly regression alert (the alerts slice).

## 5f. Builder, registry and catalog (slice B9a, the read side)

Served under `/api/plugins/console/v1`, from the migrated `catalog_*`, `library_*`, `part_refs` and `api_cache` tables:

| Route | Scope |
|---|---|
| `GET builder/summary`, `GET builder/stream` (SSE) | `tachyon.builder.read` |
| `GET builder/agents[/{id}[/used_by\|checks\|history\|resolved]]`, `builder/launches[/{id}]` | `tachyon.builder.read` |
| `GET builder/library/{kind}[/{id}[/used_by\|history]]` | `tachyon.builder.read` |
| `GET registry/projects[/{id}]` | `tachyon.builder.read` |
| `GET catalog/team-policy`, `GET catalog/imported-teams` | `tachyon.catalog.read` |

- Request and response shapes, status codes and error codes are the old build's (`internal/builderapi/openapi.yaml`).
- The catalog index runs only with `tachyon.features.catalog_index` (and a Tether address); off, the summary and
  stream serve the migrated index.
- The agents, launches and registry reads ask a configured Tether for live sessions and the registry on demand,
  as before; without Tether they report live counts as unknown and the registry answers `503`.
- `catalog/team-policy` reads Tether's `teams-policy.yaml` through the reviewed proxy (`503 configuration` without
  Tether). `catalog/imported-teams` serves the snapshot an operator imported into the old API cache, or an empty
  one.
- Not yet served (B9b, CW-20261115-0073): builder changes, the launch action, capabilities, registry writes, the
  console's object writes and the Tether provider verbs (team runs, formations, provider actions).

## 6. `/api/plugins/{id}/...`: an allowlist, no catch-all

The plugin host (Substrate `pluginhost`) forwards `/api/plugins/{id}/...` to the subprocess plugins. **Since B5 the
`pluginhost` module is not selected.** It claims all of `/api/plugins/`, which shadows Tachyon's own routes under that
prefix (Substrate gap CW-20261114-0184). Tachyon registers no plugin process today. The console plugin's slice brings
it back once the gap is fixed.

- In front of it, `internal/pluginallow` serves only the (method, path) pairs on an explicit allowlist. Every
  other path under `/api/plugins/` answers **404 `not_found`, for every caller, the owner included**, before the
  guard.
- The old build's open fallback, which forwarded every unmatched console path to Torque `/api/v1/*` with any
  method and Tachyon's own Torque credential, is gone.
- Each allowlisted pair also has its route scope on the scope table (section 5), so the module's blanket
  `tachyon.plugins` scope is never what decides access.
- **B1 ships the mechanism with an empty list: nothing is forwarded.**
- The plugin slices add their pairs. The Torque passthrough pairs the SPA uses today are below; the list is final
  when the slice that serves them lands.

| Method and path (under `/api/plugins/console/v1/`) | Scope | Note |
|---|---|---|
| `GET tasks/{id}`, `GET tasks/{id}/comments`, `GET tasks/rollup`, `GET tasks` (`fields=summary`), `GET sprints/{id}`, `GET epics/{id}` | `tachyon.torque.read` | |
| `POST messages` | `tachyon.messages.send` | |
| `GET messages/{id}`, `GET messages/thread/{id}` | `tachyon.messages.read` | |
| `GET hitl/requests`, `GET hitl/requests/{id}` | `tachyon.hitl.read` | |
| `POST hitl/requests/{id}/respond` | `tachyon.hitl.respond` | |
| `POST hitl/requests/{id}/secret` | `tachyon.hitl.respond` | plus the operator check |
| `GET workflow-runs`, `GET workflow-runs/{id}` | `tachyon.workflow.read` | |
| `POST workflow-runs/{id}/steer`, `POST workflow-runs/{id}/gates/{g}`, `POST workflow-runs/{id}/plan`, `POST checkpoints/{cid}/respond` | `tachyon.workflow.control` | |
| `POST approvals/{eventId}/decide` | `tachyon.approvals.resolve` | plus the operator check (the risk is not known here) |

## 7. Acknowledgement

The GUI track (@mig-tachyon-gui) acknowledges this contract on the B1 pull request. Later changes go through the
slice that needs them, with this file updated in the same PR.
