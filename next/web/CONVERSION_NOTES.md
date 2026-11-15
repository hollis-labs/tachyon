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

## Gaps

- app-shell has no prefix/param surface routes (exact match only, `shell.tsx` surface lookup). Worked
  around host-side (`surfacePath`); nav highlighting follows the area. Not blocking.
- No `settings.region` yet: depends on the backend registering settings groups (B1+).
- Planner path links show as text until console's autolink (file viewer chip) is ported.

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
