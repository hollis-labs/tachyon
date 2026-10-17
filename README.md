# Tachyon

Plugin-based control plane for the Hollis Labs agent fabric.

**v2** is a fresh start on [`libs/app/app-host`](https://github.com/hollis-labs/libs/tree/main/app/app-host)
(the host extracted from Chimera), scaffolded with the Folio `app-dashboard`
preset (`gui_host=chimera`, then re-pointed at app-host) and the design-kit
`kit-dashboard` 0.5.0 idiom. v1 is preserved on the `v1` branch and the
`v1-final` tag.

## Layout

```
cmd/tachyon/            host entrypoint (app-host: SPA, /api, /healthz)
internal/catalog/       reviewed first-party plugin list: env + SSE stream relays
internal/server/        plugin inventory, GET /api/plugins, SSE relay
internal/webui/         embedded UI build (dist/)
pkg/pluginkit/          plugin-side helpers (buffered upstream proxy)
plugins/<id>/main.go    plugin backend (plugin-sdk subprocess)
plugins/<id>/ui/        plugin UI package (@tachyon/<id>-ui)
packages/host-sdk/      host <-> plugin UI contract (@tachyon/host-sdk)
packages/torque-client/ shared Torque API client + types (@tachyon/torque-client)
frontend/               host shell (router, nav rail, theme, plugin registry)
docs/data-gaps.md       backend gaps for the Console port
```

## Agent builder

Builder reads the live Tether agent and launch catalogs through the Console
backend. Configure `TETHER_ADDR` and `TETHER_TOKEN_FILE` on the host; the host
passes them to the plugin. Authenticated remote access requires HTTPS. The
protocol header and token-file authentication from the earlier catalog-ops
work are reused here.

Create, clone and edit produce a local review. **Save demonstration** is a
no-op: it sends no write or approval request, does not update the directory,
and explicitly reports that write access is pending. Drafts are not persisted.
Agent prompts and system prompts remain separate; providers and projects from
launch profiles are displayed as launch configuration, not agent defaults.
Teams, assignments, templates, audit and retirement-impact reads are not yet
connected. The adapter does not substitute sample data when a read fails.

The agent-os deployment is declared in `tachyon.cerberus.yaml`. Register that
file with Cerberus, then deploy `tachyon-backend-dev` to rebuild the frontend,
plugins and host together. It serves the existing `tachyon` systemd user service
on loopback port 8093 behind `https://tachyon.nanite.cloud/builder`. Host-specific
upstream settings belong in `~/.config/tachyon/env`; `TETHER_TOKEN_FILE` must
point to a Tether credential. Fragments Engine's API token is separate and does
not authenticate Tether catalog reads.

## Plugins

| Plugin | Backend | UI routes |
|---|---|---|
| `planner` | forwards `/api/plugins/planner/v1/*` to Torque `/api/v1/*` | `/planner`, `/planner/:db`, `/planner/:db/:id` |
| `console` | forwards `/api/plugins/console/v1/*` to Torque `/api/v1/*` | `/agents`, `/builder`, `/prs/inbox`, `/pr/...` plus a shell overlay |

Request flow:

- **Buffered calls:** the browser calls `/api/plugins/{id}/...`. app-host passes these to the plugin process over `http/handle`, and the plugin calls the upstream.
- **Server-sent events:** the app-host plugin path buffers responses. The host therefore relays declared streams at `/api/plugins/{id}/stream/{name}` directly to the upstream.

Phase 1 compiles plugin UIs into the host frontend. Runtime-loaded bundles are tracked by CW-20261016-0165.

## Build and run

Requirements: Go 1.26.9 and Node 22.12+.

```sh
make all          # npm ci + UI build, plugin binaries (bin/plugins), host binary
./tachyon         # http://127.0.0.1:18093  (LISTEN_ADDR to override)
make ui-dev       # Vite on :5179 proxying /api to :18093
```

Environment variables:

| Variable | Default |
|---|---|
| `LISTEN_ADDR` | `127.0.0.1:18093` |
| `TORQUE_URL` | `http://127.0.0.1:8990` |
| `TORQUE_API_TOKEN` | unset (optional; never sent to the browser) |
| `TACHYON_PLUGIN_DIR` | `bin/plugins` |
| `TETHER_ADDR` | Tether catalog endpoint: HTTPS URL, loopback HTTP/TCP, or `unix:/path`; unset shows a configuration error in Builder |
| `TETHER_TOKEN_FILE` | Optional private `0600` bearer-token file, read by the Console backend; never sent to the browser |
| `TACHYON_DATA_DIR` | `$XDG_DATA_HOME/tachyon` |
| `TACHYON_CACHE_DIR` | `$XDG_CACHE_HOME/tachyon` |

## Porting conventions (Torque GUI → plugin UIs)

- **Parity first.** Keep Torque's markup, class strings, behaviour and copy. Change imports, not design.
- **Imports:**
  - `@hollis-labs/sysop-ui` primitives (Button, Dialog, Input, EmptyState, …) come from `@hollis-labs/design-components`. Dashboard idiom pieces (NavRail, PageHeader, StatusBadge, statusTone, ThemeSwitcher, …) come from `@hollis-labs/kit-dashboard`, which does **not** re-export design-components.
  - `/api` becomes `@hollis-labs/design-app-runtime`.
  - `/layout`, `/data`, `/widgets` and `/charts` keep the same subpaths under `kit-dashboard`.
  - 0.5.0 props can be stricter. For example, `EmptyState` requires `variant`.
- **Aliases.** Torque's `@/…` becomes `@planner/…` or `@console/…` inside each plugin. Shared Torque `lib/api`, `lib/types`, `lib/utils`, `lib/toast`, `hooks/use-api` and `hooks/use-sse` come from `@tachyon/torque-client`.
- **No cross-plugin imports.** Use host-sdk slots instead. For example, the Planner renders `<HostLinkText>`, and Console provides the autolinker through `SlotProvider`.
- **No mock or fixture data.** Where a backend is missing, render the real client's error or empty state. See `docs/data-gaps.md`.
