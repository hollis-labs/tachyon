# next/web conversion notes (GUI track)

The Tachyon SPA on Substrate: app-shell + BOM-pinned kits, built into `next/internal/webui/dist` and
embedded in the `next/` binary. Plan: `docs/substrate-port-plan.md` (G1 = CW-20261114-0114). API
contract: `next/docs/api-contract.md` (B1).

## Layout

| Path | What |
|---|---|
| `next/cmd/tachyon/ui/app.json` | The app's UI contributions (SF-2 vocabulary): one `surface` + `nav.item` per area, footer items for settings, account and identity. |
| `src/generated/frontend.manifest.json` | `npm run manifest` (`substrate frontend`), committed. Regenerate after editing `app.json` or bumping Substrate. |
| `src/App.tsx` | `IdentityProvider` → `IdentityGate` → `CapabilitiesProvider` → `BrowserRouter` → `ManifestShell`. |
| `src/surfaces.ts` | Deep link → owning surface, legacy redirects. |
| `src/api/request.ts` | The one fetch wrapper: `X-Requested-With` on every request, flat `{code, message, details?}` errors as `ApiError`, 401 → `tachyon:unauthorized`. |
| `src/api/capabilities.tsx` | Scopes from `GET /api/identity/profile`; `useCan(scope)` with the backend's matching rules. |
| `tests/` | Playwright against the real binary on a throwaway data dir (see `playwright.config.ts`). |

## Decisions

- **Routing.** app-shell 0.11.0 matches surfaces by exact path and owns no router. The host keeps
  react-router (as Tangent does) and tells the shell the owning area's route (`surfacePath`); pages
  resolve their own deep links from the URL. Deep links kept: `/planner/:db/:id`,
  `/planner/themes/:id`, `/agents/:session`, `/builder/*`, `/registry/projects/:id`,
  `/chat/:threadId`, `/pr/:host/:owner/:name/:number` (→ Repos). Legacy redirects: `/` and `/tasks*` →
  `/planner`, `/prs/inbox` → `/repos`. The surface list comes from the generated manifest, so there is
  one source.
- **Landing** stays `/planner`.
- **No elevation.** Operator is the scope `tachyon.operator` (owner via `*`), read from the profile;
  there is no step-up, no `/session`, `/elevate` or `/de-elevate` call. The high/cerberus confirm
  phrase stays with the approvals surface (G4).
- **Session loss.** A 401 on the app's own requests fires `tachyon:unauthorized`; the bridge asks the
  kit for its sessions so the kit itself moves to "Your session has ended". The profile is read again
  whenever the tab becomes visible, so an expired session is noticed on return.
- **Account and identity.** `/user` is kit-identity's account page (kit-account underneath); access
  keys, sessions and sign-out are kit-identity pages in `nav.footer` (`user.menu` is not rendered by
  app-shell 0.11.0, CW-20261112-0107). The keys page suggests the route scopes; never
  `tachyon.operator`, which is never granted to a key.
- **Theme.** One palette, `nanite-default`, with the user's light/dark/system mode (`src/mode.ts`,
  carried over from `frontend/src/mode.ts` unchanged, and the same pre-paint script).
- **Settings.** `/settings` is a `layout: settings` surface: the shell renders `settings.region`
  sections from the admin manifest's app groups (none registered yet), and the page carries
  Appearance (colour mode, browser-local) as before.
- **Kits.** Exact `bom/bom.yaml` pins (Substrate d064f53), one copy each; kit-settings 0.2.0's `^0.4`
  token/component ranges are overridden to the pinned versions (BOM `kit_exceptions`). The old
  review tarballs are not used (code-identical to these releases: GUI inventory §11).
  `plugin-host-ui` is pinned because app-shell depends on it; there is no runtime UI plugin loading.

## Planner (G2, CW-20261114-0157)

The planner is the landing surface, carried over from `plugins/planner/ui` (main at the #710 fix) into `src/planner`.
The carried files keep their own formatting and import root (`@planner/*`, aliased in tsconfig, Vite and Vitest),
so a diff against the old tree shows only the port's edits.

- **Carried:** the 49 files reachable from the live routes (themes shell, lens pages, create dialog, shared
  components) and the tests that cover them.
- **Not carried (dead in the old build):** `pages/PlannerPage.tsx`, `planner-detail-dialog`, `torque-task-dialog`,
  `planner-board`, `planner-row`, `planner-fact`, `torque-task-ref`, `workstream-tasks-panel`, `status-badge`,
  `use-planner-row-focus`, `use-planner-task-titles`, and `index.tsx` (whose route table moved into
  `src/planner/surface.tsx` unchanged). These were reachable only from the unrouted `PlannerPage` or from tests.
- **Routing:** `/planner` is one surface. The planner page owns its old route table (themes, `:db`, `:db/:id`,
  legacy slugs `board`/`flight`/`priorities`/`activity`, and `workstreams` → `streams`) inside a react-router
  `<Routes>`, as Tangent does. `surfacePath` gives the shell `/planner` for all of them, so the nav stays on Planner.
- **Edits to carried code** (each one small and named):
  - `api/client.ts`: `X-Requested-With` on every request, the flat error body (`message`, not `error`), and 401 →
    `tachyon:unauthorized`.
  - `api/errors.ts`: "may not write" is a 403 `operator_required` / `scope_required` (or the old
    `operator_access_required` until B3). A 401 is the session ending, not an operator prompt.
  - `api/notifications.ts`: `authorizing` runs the write once, with no step-up replay.
  - `components/planner/planner-write-error.tsx`: no "Authorize operator" button.
  - `shell/context.tsx`: a path link renders as text with its label. Console's autolink chip arrives with the
    files surface.
  - `api/torque-types.ts` replaces the `@tachyon/torque-client` type import.
  - `shell/frame.tsx`, `shell/lenses/inbox.tsx`: `h1` → `h2`, and `frame`'s `<main>` → `<div>`. app-shell owns
    the page's `h1` and `main`.
  - `shell/PlannerShell.tsx`, `shell/lens-page.tsx`: the wide-screen auto-focus only redirects while the browser
    is still on the planner. Before, a nav click landing between the board's commit and the effect's flush was
    replaced by the planner's redirect. Same race in the old build; covered by a unit test.
- **Lint:** Biome does not check `src/planner`, as in the old tree (the planner was never under Biome). The design
  lint (`eslint-config-design`) does, with test files ignored as the old planner lane did.
- **Parity:** #710 (a lens status change sends the item's current revision) is included. The e2e asserts the PATCH
  body.
- **Toasts:** `App.tsx` mounts `TooltipProvider` and `<Toaster position="bottom-right" />` as the old
  `frontend/src/App.tsx` did. Without it every planner toast (write failures, undo, refusals) was dropped.
- **e2e:** until B5 serves the planner from `next/`, `e2e/planner-stub.ts` answers
  `/api/plugins/console/v1/planner/*` from a seeded in-memory store behind the real binary's identity and shell.
  It enforces `expected_revision`, `X-Requested-With`, flat errors, and 404 `not_found` for unserved paths.
  `tests/fixtures.ts` installs it for every test, and records the planner writes each page sends.
  `TACHYON_E2E_PLANNER=backend` turns the stub off. The binary's own planner (B5) answers instead, seeded through
  its API with the stub's items (same ids, e2e/global-setup.ts). The stub's status sets are the backend's native
  ones (what `/schema/<db>` advertises).

## Console shared layer (G3a, CW-20261115-0009)

`src/console` and `src/packages` hold the old console's shared layer, copied from `plugins/console/ui/src` and
`packages/{torque-client,host-sdk}/src` at main **4a76422**, unchanged, so later console surfaces (chat first)
build on it. Diff any file against its source at that SHA; the only edits are below.

- **What:** the files the chat surface's shared dependencies reach that do not themselves need chat-only or
  uncarried modules: messaging (types, context, adapter, unread), autolink and refs (reference chips, previews,
  markdown), the preview panes those open (forge, events, observations-free parts, builder, agents, files,
  tesseract), seams, lib, the torque client and the host SDK, and `chat/MessageBody` (the shared message
  renderer the Tesseract detail pane uses). Their unit tests and test helpers come along.
- **Edits:** example strings in `refs/tach.ts`, `refs/tach.test.ts` and `chat/MessageBody.test.tsx` named an
  internal host; they are placeholders (`example-app`) for the public repo (scan-public). Nothing else.
- **Not wired:** nothing in the app imports this layer yet; the chat PR (G3) does.
- **Build:** aliases `@console/*`, `@tachyon/torque-client`, `@tachyon/host-sdk`; dependencies at the old tree's
  versions (streamdown 2.7.0, react-markdown 10.1.0, remark-gfm 4.0.1, yaml 2.9.1, sonner 2.0.8).
- **Lint:** as in the old tree, Biome does not check `src/console` or `src/packages`, and the design lint covers
  `src/console/agents` only (the old builder lane).

## Chat (G3, CW-20261115-0009)

The chat surface, carried over from `plugins/console/ui` (main 4a76422, incl. #707, #715, #721/#725) into
`src/console`, with the shared packages it uses (`packages/torque-client`, `packages/host-sdk`) in `src/packages`.
Carried files keep their formatting and import roots (`@console/*`, `@tachyon/torque-client`, `@tachyon/host-sdk`,
aliased), so a diff against the old tree shows only the port's edits.

- **Carried:** the 197 files reachable from the chat page and the providers it needs, plus their tests
  (and the test helpers those need). That includes the shared console layers chat renders through: messaging,
  autolink and refs (message reference chips) and the preview panes those open (forge, events, observations,
  builder, agents). Later console surfaces reuse them.
- **Not carried (out of G3):** the chat dock (`ChatDock`), the PTY terminal (`drawer/TerminalPane`, `PtyTerminal`),
  the app-agent thread source (`appagent`, `composite`), the alert host, the card runtime, HITL, workflow,
  approvals and the operator prompt. Tests of uncarried modules are not carried.
- **Providers:** `src/console/surface.tsx` composes, from the old `shell.tsx` and `ConsoleIntegration.tsx` in
  the same order: torque ApiProvider → autolink → LinkText slot → messaging → agent unread → chat (Tether mail
  + chat API summaries and messages). Routes `/chat` and `/chat/:threadId` resolve inside the page.
- **Seams (edited so the carried code is unchanged elsewhere):**
  - `operator/elevation.ts` is replaced: `operatorFetch` is the host fetch (no step-up; contract section 5).
  - `packages/host-sdk` `hostFetch`: `X-Requested-With` on every request; a 401 fires `tachyon:unauthorized`.
  - `lib/http.ts` `consoleJson`: the flat `{code, message}` error body first (the old nested shape still read).
- **Other edits:**
  - `chat/view/ConversationView.tsx`: no terminal pane (the drawer's terminal mode says so).
  - `shell/chat-dock.tsx`: the provider no longer renders the dock.
  - `chat/index.ts`: no dock / app-agent / composite exports.
  - `chat/view/ChatView.tsx`: the conversation pane is a `section` (app-shell owns `main`).
  - `chat/workbench/Header.tsx`: the title is `h2` (the view test reads level 2).
  - `chat/roster.ts` `chatJSON`: a 204 (plan delete) succeeds. The old build threw "invalid response", so every
    successful plan delete showed an error; same code there. Unit-tested (fails without the fix).
- **Kit wins (G3 review):** `messaging/QuickActions.tsx` renders through design-components `Popover` and `Dialog`
  instead of its hand-rolled overlays (focus trap and return, portal, layered Escape). The other overlaps stay app
  UI until their kit gaps land (CW-20261115-0031 kit-cards, -0032 kit-chat, -0033 design-components); the carried
  directories and the review are listed in `next/docs/conversion-notes.md`.
- **Errors:** chat reports failures inline (`role="alert"` by the composer, the plan, the message), as in the old
  build; it raises no toasts. The e2e asserts the backend's own message is shown and the draft kept.
- **Lint:** Biome skips `src/console` and `src/packages`, as in the old tree; the design lint covers
  `src/console/agents` only (the old builder lane).
- **e2e:** `e2e/chat-stub.ts` answers the chat routes behind the real binary until B6 serves them; in stub mode
  the chat and planner streams are quiet open EventSources (a stubbed stream would close and show
  "Reconnecting"). `TACHYON_E2E_CHAT=backend` turns it off.

### Chat routes the surface calls (for B6)

All under `/api/plugins/console/v1`. Errors are the contract's flat body; writes carry `X-Requested-With`. Checked
against the old handlers by B6's builder (#744 review); the corrections from that check are in.

- **`expected_revision`:** in the body for add step, patch step, move step and `PUT .../plan`; in the query for
  plan delete (400 if missing or not an integer). **Create plan and reorder take none**, and both refuse unknown
  fields with 400; the client sends `{title}` and `{plan_ids}` only. A mismatch is 409 `conflict`. Plan delete
  answers 204, no body.
- **Streams:** plain `EventSource`s of unnamed `data: {type, thread_ref, at, ...}` frames. `/chat/events` sends
  only `thread` frames, without `id:`, keepalive every 15 s. On the per-thread stream only hub frames carry `id:`
  (`message`, `delivery`, `activity`, `plan`, `card`, `topic`, `bookmark`, `workspace`, `terminal.run`); `panel`,
  `context`, `thread`, `delta`, `tool` and `end` do not. Replay covers the last 500 events per thread; a bad
  `Last-Event-ID` is 400.
- **Empty vs unknown:** a thread that exists answers 200 with an empty list on `/plans` and `/topics`; 404 means
  the thread is unknown. The UI treats 404 `code: not_found` (and the old message match) as an empty list.

| Method | Path | Transport | Request | Response the UI reads |
|---|---|---|---|---|
| `GET` | `/chat/threads?limit=200` | rest | - | {threads:[{thread_ref,title,agent:{agent_id},unread,last_at,last_preview,blocked_on_user,blocked_breakdown:… |
| `PATCH` | `/chat/threads/:ref` | rest | {pinned} | thread object (ignored; UI re-lists) |
| `GET` | `/chat/threads/:ref/messages?limit=20[&before_seq=N]` | rest | - | {messages:[{seq,id,role:operator\|agent\|system,kind,text,at,meta:{input_classes[],input_sources[],input_tr… |
| `POST` | `/chat/threads/:ref/messages` | rest | {text,idempotency_key} | **202** {id,seq,idempotency_key,state:"accepted"} (+`reply_id` on app threads); 409 `conflict` (session not waiting; bridge), `busy`. No key/text 409 here (that is `POST /chat/agents/:launch/send`) |
| `POST` | `/chat/threads/:ref/read` | rest | (none) | {ok:true} (ignored) |
| `POST` | `/chat/threads/:ref/stop` | rest | (none) | {thread_ref,state:"stop_requested",interrupt} |
| `GET` | `/chat/events` | sse | - | data frames {type:"thread",thread_ref,at,blocked,unread,plan_progress,pinned} |
| `GET` | `/chat/threads/:ref/events` | sse | - | data frames {type,thread_ref,at,...payload flat} |
| `GET` | `/chat/threads/:ref/plans` | rest | - | {plans:[{id,position,title\|null,status,revision,task_ref\|null,steps:[{id,position,title,status:todo\|doin… |
| `POST` | `/chat/threads/:ref/plans` | rest | {title:string\|null} | 201 Plan {id,revision,...steps} |
| `POST` | `/chat/threads/:ref/plans/reorder` | rest | {plan_ids:[...]} | {plans:[Plan]} |
| `DELETE` | `/chat/threads/:ref/plans/:planId?expected_revision=N` | rest | (none) | 204 no body |
| `POST` | `/chat/threads/:ref/plans/:planId/steps` | rest | {title,expected_revision} | 201 Step {id,position,title,status,task_ref,plan_revision} |
| `PATCH` | `/chat/threads/:ref/plans/:planId/steps/:stepId` | rest | {status,expected_revision} | Step {...,plan_revision} |
| `POST` | `/chat/threads/:ref/plans/:planId/steps/:stepId/move` | rest | {to_plan,position?,expected_revision,to_expected_revision} | {from:Plan,to:Plan,step:Step} (the moved `step` has no `plan_revision`) |
| `GET` | `/chat/threads/:ref/plan` | rest | - | Plan {id,title,updated_at,created_at,steps:[{id,title,status,position}]} |
| `GET` | `/chat/threads/:ref/cards` | rest | - | {cards:[{item_id,kind:card.approval\|card.tool_approval\|card.question,caption,fields:[{id,type,label,optio… — no `created_at`; also `cite`, `actions`, `expires_at`, `presented_projection_revision`, `op_digest`; `target` {scheme,ref,op}\|null; `result` {decision,by,at,note?,answers?}; kinds also `card.plan`, `card.dispatch`, `card.notice` |
| `GET` | `/chat/threads/:ref/topics` | rest | - | {topics:[{id,title,message_ref,seq,added_by,created_at}]} |
| `POST` | `/chat/threads/:ref/topics` | rest | {title,message_ref} | Topic (ignored) |
| `GET` | `/chat/threads/:ref/bookmarks` | rest | - | {bookmarks:[{id,message_ref,note,created_at}]} |
| `POST` | `/chat/threads/:ref/bookmarks` | rest | {message_ref} | Bookmark (ignored) |
| `DELETE` | `/chat/threads/:ref/bookmarks/:id` | rest | (none) | {ok:true} |
| `GET` | `/chat/threads/:ref/work[?agent=:agentId]` | rest | - | {items:[{id,kind:theme\|task\|decision,title,status,progress,counts:{done,doing,blocked,todo}\|null,needs_u… — `counts` absent (not null) when empty |
| `GET` | `/chat/threads/:ref/activity?limit=50[&before_seq=N]` | rest | - | {activity:[{seq,type,actor,cite:[{kind,ref,label}],payload:{title,summary,body,review_ref}\|null,at}],has_m… — `limit` 1..100 |
| `POST` | `/chat/threads/:ref/activity/comments` | rest | {body} | Activity row (ignored) |
| `GET` | `/chat/threads/:ref/approval-rules` | rest | - | {rules:[{id,tool,effect,created_at}]} |
| `DELETE` | `/chat/threads/:ref/approval-rules/:id` | rest | (none) | {ok:true} |
| `GET` | `/chat/threads/:ref/workspace` | rest | - | {items:[{id,type:file\|link\|ref\|image,name,mime,size_bytes,url,ref,shared,added_by}],limits?} |
| `POST` | `/chat/threads/:ref/workspace` | rest | multipart/form-data field file | WorkspaceItem (ignored; reload) |
| `PATCH` | `/chat/threads/:ref/workspace/:id` | rest | {shared} | WorkspaceItem (ignored) |
| `DELETE` | `/chat/threads/:ref/workspace/:id` | rest | (none) | {ok:true} |
| `GET` | `/chat/threads/:ref/workspace/:id/content` | rest | - | raw bytes (text or blob) |
| `GET` | `/chat/threads/:ref/scratchpad` | rest | - | {body,revision} |
| `PUT` | `/chat/threads/:ref/scratchpad` | rest | {body,expected_revision} | {body,revision} |
| `GET` | `/chat/settings` | rest | - | {scratchpad_placement:drawer\|aside,...} |
| `PUT` | `/chat/settings` | rest | {scratchpad_placement} | Settings (ignored) |
| `GET` | `/chat/threads/:ref/continue` | rest | - | {continue:boolean} |
| `PUT` | `/chat/threads/:ref/continue` | rest | {continue} | {continue:boolean} |
| `GET` | `/chat/agents` | rest | - | {agents:[{launch_id,agent_id,display_name,channel,worker?,session:{id,state,last_activity_at,at_turn_bounda… — proxied to Tether (query dropped); shape is Tether's |
| `GET` | `/chat/outbox?launch_id=:launchId` | rest | - | bare array [{key,launch_id,body,at,status,error?,session_id?}] |
| `POST` | `/chat/agents/:launchId/send` | rest | {text,idempotency_key} | {session_id,(replayed)} (ignored) |
| `GET` | `/chat-turns/inbox` | rest | - | {pending:[Turn],history:[Turn]} Turn={item_id,agent_id,session_id,turn_id,kind,content,created_at(ISO),revi… — `attribution` values are comma-separated strings; `X-Tachyon-Cache` is `live`, `stale` or `error` |
| `POST` | `/chat-turns/items/:itemId/reply` | rest | {expected_revision,action:"respond",response_text} | Turn (delivery_state read) |

**Served by B6 but not called by this SPA:** `POST /chat/threads`, `GET /chat/threads/{ref}`, the single-plan writes
under `/threads/{ref}/plan` (PUT, DELETE, steps, reorder; the SPA reads `GET .../plan` only), `DELETE
/threads/{ref}/topics/{id}`, `GET /threads/{ref}/context`. There are no cursor routes. Every chat write,
`POST .../read` included, needs the operator check; workspace `content` reads do too.

**Later B** (reached from chat, owned by other slices; until they land these degrade to the UI's not-found handling):
`GET /terminal/runs`, `PATCH /terminal/runs/:id`, `POST /terminal/runs/:id/stop` (terminal slice);
`GET /approval-inbox/items/:id`, `POST /approval-inbox/items/:id/resolve` (approvals slice; the old client's
`POST /approval-inbox/session` retry is the elevation session the contract removed, to drop there);
`GET /planner/board`, `/planner/need-you`, `/planner/decisions` (B5); `GET /files/roots`, `/files/list` (files);
`GET /catalog/agents`, `/catalog/launches` (catalog); `POST /messages` (Torque passthrough allowlist).

## Approvals (G4, CW-20261115-0024)

The approvals surface, carried over from `plugins/console/ui/src/approvals` (main f256039; the area is unchanged
since 4a76422) plus four small shared components (`components/chip`, `initials-avatar`, `keycap`, `pill-tabs`),
onto the console layer from G3a. `src/console/surface.tsx` mounts it as `/approvals` under the console base with
the `ApprovalProvider` the old `ConsoleIntegration` placed above every console page.

- **Not carried:** `approvals/terminal.tsx` (the action-run PTY; terminal slice). The detail says so where the run
  panel was.
- **Edits (scripted, `g4-adapt`):**
  - `client.ts`: no session bridge. A 401 is the session ending (hostFetch hands it to the identity gate), not a
    cue to `POST /approval-inbox/session` and retry; that route is gone (contract section 1).
  - `page.tsx`: batch "operator access needed" comes from the profile's `tachyon.operator` scope (`useScopes`),
    not `GET /access` (elevation state). Refusals accept `operator_required`, `scope_required` and the old
    `operator_access_required` / `assurance_insufficient`; copy says "operator access", not "elevated".
  - `secret-dialog.tsx`, `batch-panel.tsx`: the same codes and copy.
  - **Kit wins:** `PillTabs` is kit-dashboard `DashboardViewTabs variant="pills"`; `components/pill-tabs.tsx` is
    removed (the smaller filter-row size is the kit's gap, CW-20261115-0033).
  - `context.tsx`: **a held decision the server refuses is a toast.** The old code set the queue error, then
    refreshed, and the refresh cleared it at once: an operator refusal of a held (low/medium) approval flashed and
    vanished, worded "Approval queue unavailable". Same code in the old build; the e2e covers it.
  - `client.ts` `decisionError`, used by `detail.tsx` and `context.tsx`: **an access refusal of a single decision
    is worded by the app**, by code (`operator_required`, `scope_required` and the old codes), as batch and
    secret refusals already were. The old decision bar showed the server's message; B7's is written for API
    callers ("an API key never qualifies"). Other errors keep the server's message.
  - **Kit wins:** the approvals `Chip` is design-components `Pill` (kit look; `faint` becomes `neutral`);
    `components/chip.tsx` is removed.
  - Tests: the two unit tests that asserted the session retry and `/access` now assert the port's behaviour.
- **Kit gap:** the kit's `PageHeader` always renders an `h1`, so the page has two (the shell's and the kit's).
  Substrate CW-20261115-0026; not worked around (no redesign).
- **e2e:** `e2e/approvals-stub.ts` stands in for B7; `TACHYON_E2E_APPROVALS=backend` turns it off and runs against the
  binary, with these differences:
  - `e2e/serve.sh` applies `e2e/approvals-seed.sql` with sqlite3, after the owner setup has made the schema
    and before serving. These are synthetic `approval_items` rows; B7 backfills the index at start, and there is
    no producer path. It also writes a throwaway `tachyon.files.secret_key` for the run.
  - The specs find items by title. The tests that decide a request run last, because the backend keeps its
    state between tests.
  - The refusal is real. The owner's session mints a token without `*`: the same person, below operator.
  `store.refuseOperator` makes decisions answer 403 `operator_required`.

### Approvals routes the surface calls (for B7)

All under `/api/plugins/console/v1/approval-inbox`. Errors are the contract's flat body (the hitl fields
`revision_kind`, `expected_revision`, `actual_revision`, `current_state`, `terminal_outcome` under `details`); writes
carry `X-Requested-With` and a same-origin `Origin`. Checked against the old handlers by B7's builder (#750 review);
the corrections from that check are in. Refusals come from the route guard: 401, then 403 `scope_required`
(`tachyon.approvals.read` / `.resolve`), then 403 `operator_required` where elevation was needed (medium, high,
destructive and cerberus resolves, batches, secrets, the `action_run` view); `unsupported_class` (403) stays.

| Method | Path | Request | Response the UI reads |
|---|---|---|---|
| `GET` | `/inbox[?pending_before&history_before]` | — | `{pending:[Item], history:[Item], next_pending?, next_history?, synced_at, revision, server_now_ms}`. Item: `{item_id, state, revision, presented_projection_revision, enqueued_at, updated_at, approvable, expires_at, batchable?, batch_blockers?, confirm?:{phrase}, affected_*?, alternatives?, run_state?, reason_required?, deferred_until?, terminal_outcome?, action_run?, evidence?, request_snapshot:{kind, title, summary, request, class, source:{agent_id, application_id}, impact:{approve, deny}, data?, expires_at?, handoff_url?}}` |
| `GET` | `/items/:id` | — | Item (a no-cache read before deciding; `X-Tachyon-Cache` `stale`/`error` is refused) |
| `POST` | `/items/:id/present` | `{expected_revision, presented_projection_revision, connection_id}` | the Item as read, **state unchanged** (records a `presented` audit event once per session and item); 409 `stale_revision` |
| `POST` | `/items/:id/resolve` | `{expected_revision, presented_projection_revision, response:{kind, decision, note?, confirmation?, alternative_id?, answers?}}` | `{item_id, state, reason?, message?, resolution?}`; 409 `stale_revision`; 403 `operator_required` / `scope_required` (checked before the phrase); 422 `confirmation_required` (only with `confirm.phrase`, an approval, and no `alternative_id`); `provider_handoff_required` |
| `POST` | `/batch/resolve` | `{batch_id, decision, items:[{item_id, expected_revision, presented_projection_revision}]}` | `{batch_id, results:[{item_id, status, error?}], counts}`; a stale item is `status: "stale"` (`error.code stale_revision`), not an HTTP 409 |
| `POST` | `/items/:id/defer` | `{duration_s, reason?, expected_revision}` | `{item_id, deferred_until, expires_before_snooze_ends?, revision}`; checks `expected_revision` only |
| `DELETE` | `/items/:id/defer` | — | `{item_id, deferred_until, revision}` (no revision check; no `expires_before_snooze_ends`) |
| `GET` | `/items/:id/audit` | — | `{item_id, events:[AuditEvent]}` |
| `GET` | `/history[?q&status&requester&cursor&limit]` | — | `{events:[HistoryEvent], next_cursor?, facets}` (`facets` always present) |
| `POST` | `/secrets` | JSON `{item_id, field, value}` (≤16 KiB) or multipart `file` (≤64 KiB); optional `Idempotency-Key` | **201** `{secret_ref, created_at, expires_at, size, fingerprint}`, never the value; 409 `item_not_pending` / `secret_field_unknown` / `secret_limit` / `idempotency_key_reuse`; 413 `too_large`; 429 `rate_limited` |
| `GET` | `/events` | SSE: `id: <n>` + `data:` (no `event:` line), ids and states only | any frame means refetch; resume with `Last-Event-ID` or `?after=`; a `resync {reason:"gap"}` frame means refetch everything |

Not called by the port's SPA: `/session`, `/status`, `/elevate`, `/de-elevate`, `/access` (gone with the Tangent
cookie; operator is the profile scope), and `/items/:id/evidence/...` (does not exist; evidence is the item's
`evidence` array; the client's unused `evidence()` call is never made).

## Agent Ops (G5, CW-20261115-0057)

Carried byte-identical from **main 99c7b88** (not 4a76422 like the shared layer, so the agents timeline
(#728-#730), seats and needs-you (#718/#719/#726) and the ceremony meter are in):
- `agents-monitor/`, with its tests and fixtures;
- `needs-you/`;
- `fleet/LaunchAgentDialog.tsx`, `fleet/launch-client.ts`, and `fleet/tether-sessions.ts` (its test imports it).
The rest of their import graph was already carried in G3a.

- **Surfaces.**
  - `AgentsSurface` (`/agents`, `/agents/:session`).
  - `NeedsYouSurface` (`/needs-you`): a new nav item and surface in `ui/app.json`, after Agent Ops as on old main. It requires `tachyon.agents.read` (accepted for B8, CW-20261115-0062).
- **Edits** (scripted, `g5-adapt.py`):
  - `teams-tab.tsx`: G5 made it a note; G6 restores the old file and its test (team operations in the Agent Ops
    Teams tab, where the old UI had them).
  - `activity-tab.test.tsx`: a host name in a test string is a placeholder.
- **Kit:** the page header's double `h1` (kit gap CW-20261115-0026), as on the other surfaces. Nothing else in
  the area has a kit equivalent beyond what it already uses (design-components, kit-dashboard layout).
- **Controls** show a refusal inline (`role="alert"` in the panel), not as a toast. Carried as is.
- **e2e:** `e2e/agents-stub.ts`, the contract stub. It serves the old contract stub's fixtures
  (`internal/agentsapi/fixtures` at 99c7b88, in `e2e/agents-fixtures`, with two host strings made placeholders).
  `TACHYON_E2E_AGENTS=backend` turns it off and runs against the binary's agents reads (B8a). The collector
  is not started there, so `e2e/serve.sh` applies `e2e/agents-seed.sql` before serving. It holds synthetic sessions,
  events, a gap and seat statuses, with the stub's names and times relative to the run. The Tether fake
  (`e2e/tether-fake.ts`) also starts, with `agents_controls` on and a throwaway messages token, so a nudge goes out as
  Tether mail (B8b). The fake refuses a message containing REFUSE, and the spec asserts B8b's own refusal. Ceremony
  needs a configured Torque, so it is skipped there.

**Main moved under the shared layer since 4a76422** (for the pre-P1 sync):
- `chat/MessageBody.tsx` (+6)
- `files/FileViewerPane.tsx` (1 line)
- `observations/store.ts` (+49/-4, #762: one request for every work state)
The G5 files have no main change after 99c7b88.

### Agent Ops routes the surface calls (for the agents backend slice)

All under `/api/plugins/console/v1`. Errors are flat `{code, message}`; writes carry `X-Requested-With` and a
same-origin `Origin`. Shapes: the old written contract, `internal/agentsapi/openapi.yaml` and its fixtures at
99c7b88. The handler column is the old build's.

| Method | Path | Request | Response the UI reads | Old handler |
|---|---|---|---|---|
| `GET` | `agents/sessions` | — | `{sessions:[Session], summary, collector}` | `internal/agentsserve/api.go` `s.list` |
| `GET` | `agents/stream` | SSE | session changes; the list refetches | `agentsserve` `s.stream` |
| `GET` | `agents/sessions/{id}` | — | Session (a deep link to a session not in the list) | `agentsserve` `s.get` |
| `GET` | `agents/sessions/{id}/events?group_runs=1&limit=8\|100[&before_seq]` | — | `{events, gaps, next_before}` | `agentsserve` `s.events` |
| `GET` | `agents/sessions/{id}/context-series` | — | `{points, compactions, clears, warn_pct}` | `agentsserve` `s.contextSeries` |
| `GET` | `agents/sessions/{id}/tokens` | — | `{metered, hour, session, share, trend}` | `agentsserve` `s.tokens` |
| `GET` | `agents/sessions/{id}/history` | — | `{sessions}` | `agentsserve` `s.history` |
| `GET` / `PUT` | `agents/settings` | PUT: settings | `{stale_after_s, handoff_prompt, handoff_timeout_s, nudge_presets}` | `agentsserve` `s.getSettings` / `s.putSettings` |
| `GET` | `agents/seats` | — | `{seats:[{agent_urn, handle, name, effective, declared, live, stale?, set_by_other?}]}` | `agentsserve` `s.seats` |
| `GET` | `agents/bus/events?limit=50[&before_seq]` | — | `{events, next_before}` | `internal/agentscollector/bus.go` `BusHandler` |
| `POST` | `agents/sessions/{id}/nudge` | `{request_id, text, interrupt}` | ControlRequest `{request_id, kind, session_id, state, step, error, interrupt, message_id, created_at, updated_at}` (202); 409 `conflict`, `interrupt_unsupported` | `agentsserve` `s.nudge` |
| `POST` | `agents/sessions/{id}/restart` | `{request_id, handoff, expected_session_id}` | ControlRequest | `agentsserve` `s.restart` |
| `GET` | `agents/controls/{request_id}` | — | ControlRequest (polled) | `agentsserve` `s.getControl` |
| `POST` | `agents/controls/{request_id}/skip` | — | ControlRequest | `agentsserve` `s.skipControl` |
| `GET` | `ceremony` | — | CeremonyMeter `{checked_at, window_days, gates, median_wait_seconds, stalled, pending_checkpoints, activity, done, previous, comparison, sources}` | `internal/server/register_ceremony.go` |
| `GET` | `needs-you` | — | `{items:[{id, source, kind, why, title, from, ref?, since, critical, message_id?, detail?, link?}], sources, checked_at}` | `internal/server/register_needsyou.go` (`internal/needsyou`); its `RequireOperator` passes GETs through, so no operator check on reads |
| `GET` | `catalog/launches` | — | `{launches:[LaunchProfile]}` (Launch dialog) | `plugins/console/catalog.go`, Tether `/catalog` proxy |
| `GET` | `tether/workers` | — | `{workers}` | `plugins/console/objects.go`, Tether proxy (operator) |
| `POST` | `tether/sessions` | `{launch, worker?, prompt_append?, idempotency_key}` | `{id}` | same |
| `GET` | `tether/sessions[?cursor]`, `tether/sessions/{id}` | — | `{sessions, next_cursor?}`, TetherSession | same |
| `POST` | `tether/sessions/{id}/launch` | `{}` | (ignored) | same |

The `agents/*` scopes are in the contract (`tachyon.agents.read`, `tachyon.agents.control` with the operator
check on control). `ceremony` and `needs-you` (B8a, CW-20261115-0062): `tachyon.agents.read`. For parity, there is no operator check on
`needs-you` reads: the old build mounted it behind `RequireOperator`, which passes GETs through. Not called by this SPA: `agents/collector` (the old stub's only).

## Builder and Registry (G6, CW-20261115-0063)

Carried byte-identical from **main 4b5e397**:
- `builder/` and `registry/`, with their tests;
- the `agents/` files they reach that G3a did not carry (agent editor, team formation and operations, owned builder
  and provider-action clients, plan review);
- `agents-monitor/teams-tab.tsx` and its test, back as the old file.

- **Surfaces.**
  - `BuilderSurface` (`/builder` redirects to `/builder/agents`; `/builder/agents[/:id]`, `/builder/teams`,
    `/builder/launches`, `/builder/library[/:kind[/:id]]`; `/builder/projects` redirects to `/registry/projects`).
  - `RegistrySurface` (`/registry`, `/registry/projects[/:id]`).
  - `/builder/classic` is not ported (plan section 13).
  - **Team operations** are back in the Agent Ops Teams tab, as in the old UI. Builder keeps team definitions
    (`/builder/teams`), also as before.
- **Edits** (scripted, `g6-adapt.py`):
  - Kit wins, as in G4: `Chip` is design-components `Pill` in builder and registry (`faint` becomes `neutral`);
    `components/chip.tsx` stays removed.
  - `agents/owned-builder-client.ts` reads the contract's flat `message` first, then the old `{error}` string.
- **Elevation:** writes go through `operatorFetch`, which has been the host fetch since G3. The old 401
  `operator_session_required` key prompt and replay are gone, and a 401 goes to sign-in. `StepUpDialog` is the
  mock identity seam only.
- **e2e:** `e2e/builder-stub.ts`. It serves the old contract stub's fixtures (`internal/builderapi/fixtures` at
  4b5e397, in `e2e/builder-fixtures`, with a project name made a placeholder). The data-verb envelopes follow
  `internal/dataverbs`. `TACHYON_E2E_BUILDER=backend` turns it off.

**Main moved under the carried layer since 99c7b88** (for the pre-P1 sync): `chat/aside/FileTree.tsx`,
`chat/aside/Panels.tsx` (#767), and `observations/store.ts` again (#770, #771).

### Builder, Registry and team routes the surfaces call (for the builder/catalog backend slice)

All under `/api/plugins/console/v1`. Errors are flat `{code, message}`; writes carry `X-Requested-With`. Shapes: the
old written contract, `internal/builderapi/openapi.yaml` and its fixtures at 4b5e397.

| Method | Path | Old handler |
|---|---|---|
| `GET` | `builder/summary` | `internal/server/server.go` `catalogIndex.SummaryHandler` (operator) |
| `GET` | `builder/stream` (SSE) | `server.go` `catalogIndex.StreamHandler` (operator) |
| `GET` | `builder/agents[?q&cursor…]`, `builder/agents/{id}`, `…/used_by`, `…/checks`, `…/history`, `…/resolved` | `internal/builderread/read.go` (`r.agents`, `r.agent`, `r.usedBy`, `r.checks`, `r.history`, `r.resolved`) |
| `GET` | `builder/launches`, `builder/launches/{id}` | `builderread` `r.launches`, `r.launch` |
| `GET` | `builder/library/{kind}`, `…/{id}`, `…/{id}/used_by`, `…/{id}/history` | `builderread` `r.libraryList`, `r.libraryItem`, `r.libraryUsedBy`, `r.libraryHistory` |
| `GET` / `POST` | `builder/changes`, `builder/changes/{id}`; `POST …/{id}/submit`, `…/cancel`, `…/rearm`, `…/check`, `…/mark_planned`; `GET …/{id}/bundle` | `internal/builderchange/http.go` |
| `POST` | `builder/launches/{id}/launch` | `builderchange` `h.launch` |
| `GET` | `builder/capabilities` | `builderchange` `h.capabilities` |
| `GET` | `registry/projects`, `registry/projects/{id}` | `builderread` `r.registryList`, `r.registryItem` (Tether registry, cached 10 s) |
| `POST` / `PATCH` | `registry/projects`, `registry/projects/{id}`; `POST`/`DELETE` `…/{id}/link` | `internal/builderread/registrywrite.go` (`registryCreate`, `registryPatch`, `registryLink`, `registryUnlink`) |
| `GET`/`POST` | `data/providers/tachyon/verbs/objects.{list,get,history,create,patch,delete,restore,take_ownership}` (reads take `?input=<json>`) | `internal/dataverbs` (`local.go`), behind the operator check (`server.go` `dataverbs.NewHTTP`) |
| `GET`/`POST` | `data/providers/tether/verbs/{teams.roster,teams.form,teams.preview,teams.apply,actions.list,actions.get,actions.apply,actions.reconcile,runtime.preview,agents.publish_preview,catalog.apply_preview}` | `internal/dataverbs/actions.go` and `internal/provideractions` |
| `GET` | `catalog/imported-teams` | `internal/apicache/team_import.go` |
| `GET` | `catalog/team-policy` | `plugins/console/catalog.go` (Tether `/catalog` proxy) |

The data verbs answer `{version: 1, provider, verb, data}`. Scopes (contract): `builder/*`, `registry/*` take
`tachyon.builder.read` / `.write`, with the operator check on writes. `catalog/*` takes `tachyon.catalog.read`. The
data verbs are checked in the data handler (contract, "Agent data verbs").

## Gaps

- app-shell has no prefix/param surface routes (exact match only, `shell.tsx` surface lookup). Worked
  around host-side (`surfacePath`); nav highlighting follows the area. Not blocking.
- No `settings.region` yet: depends on the backend registering settings groups (B1+).
- Planner path links show as text until console's autolink (file viewer chip) is ported.
- Chat: no dock, no PTY terminal, no app-agent thread source yet (later G slices).
- Approvals: no action-run terminal (terminal slice); two h1s under the kit PageHeader (CW-20261115-0026).

## Parity list (behaviour the port must not copy)

- **Planner lens page status change** sends `expected_revision: item.rev + 1`
  (`plugins/planner/ui/src/shell/lens-page.tsx:239`), likely a 409 on every status change from that
  control. Filed on the live app as CW-20261114-0115. The planner port (G2) passes `item.rev`.

## Running it

```sh
npm ci
npm run manifest            # after editing ../cmd/tachyon/ui/app.json
npm run typecheck && npm run lint && npm test
npm run build               # → ../internal/webui/dist (keeps .gitkeep)
# build the next/ binary (cd .. && go build -o tachyon ./cmd/tachyon), then:
PLAYWRIGHT_CHANNEL=chromium npm run test:e2e
# dev server: no default backend on purpose. Point it at a next/ binary you started on a scratch
# port and its own data directory (never a running Tachyon):
TACHYON_DEV_BACKEND=http://127.0.0.1:<port> npm run dev
```

The browser suite makes a fresh run directory (mkdtemp, marked) under `TACHYON_E2E_DIR` or the OS
temp dir, starts two backends on 28193/28194 (`TACHYON_E2E_PORT`) through `e2e/serve.sh`, and deletes
nothing. A busy port fails the run rather than testing whatever answers there.
