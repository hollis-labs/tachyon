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

## 6. `/api/plugins/{id}/...`: an allowlist, no catch-all

The plugin host (Substrate `pluginhost`) forwards `/api/plugins/{id}/...` to the subprocess plugins.

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
