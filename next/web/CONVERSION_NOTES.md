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

## Gaps

- app-shell has no prefix/param surface routes (exact match only, `shell.tsx` surface lookup). Worked
  around host-side (`surfacePath`); nav highlighting follows the area. Not blocking.
- No `settings.region` yet: depends on the backend registering settings groups (B1+).

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
# build the next/ binary, then:
TACHYON_E2E_BIN=../tachyon PLAYWRIGHT_CHANNEL=chromium npm run test:e2e
```
