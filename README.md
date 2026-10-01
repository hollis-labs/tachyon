# Tachyon

Headless-capable, plugin-based control plane for the Hollis Labs agent fabric

A **Sysop UI** application: a React frontend built on
[`design-kit`](https://github.com/hollis-labs/design-kit), using design-components'
AppShell and kit-dashboard's navigation and theme, served by a
Go binary through the [`go-webui`](https://github.com/hollis-labs/go-webui)
embed harness.

## Layout

```
cmd/tachyon/   Go entrypoint — HTTP server + /api
internal/webui/             //go:embed all:dist + the go-webui handler
frontend/                   Vite + React frontend (the Sysop UI)
  src/App.tsx               app shell — nav rail + page header
  src/pages/                one page per screen
  src/api/                  same-origin API client + typed context
```

The frontend builds into `internal/webui/dist/`, which the Go binary
embeds — so a single binary serves both the API and the UI.

## Prerequisites

- Go 1.26.1+
- Node.js 20+ / npm

## Develop

Two processes during development:

```sh
make run      # Go server on :8093 (serves /api and the last UI build)
make ui-dev   # Vite dev server with hot reload — proxies /api to :8093
```

Open the Vite dev server URL — the app is served under
`/sysop/`, not the root path.

## Build a release binary

```sh
make all      # ui-build (vite → internal/webui/dist) then build
./tachyon
```

The Sysop UI is then served at <http://localhost:8093/sysop/>.
Before the first `make ui-build`, `go-webui` serves a "not built"
placeholder in place of the app.

| Command | What it does |
|---|---|
| `make ui-build` | Build the frontend into `internal/webui/dist` |
| `make ui-dev` | Run the Vite dev server (hot reload) |
| `make build` | Build the Go binary |
| `make all` | `ui-build` then `build` |
| `make run` | Build and run the server |
| `make install` | Build the embedded UI + install `tachyon` to `$GOBIN` |
| `make test` / `make vet` | Go test / vet |

## Shutdown

On SIGINT or SIGTERM, Tachyon stops accepting HTTP connections and gives active
requests a separate five-second drain grace. If draining times out, it closes
remaining connections before unloading plugins. Each plugin then gets its own
five-second unload grace; a hung hook is force-stopped and reaped, and later
plugins still get their grace. Logs identify `http_drain` or `plugin_unload`
timeouts and the affected plugin ID. A clean signal stop exits zero. Normal
shutdown can therefore take five seconds plus five seconds per loaded plugin,
excluding bounded lifecycle-lock acquisition. Startup rollback retains its
separate shared five-second cleanup cap.

## Adding a page

A page is generic kit chrome plus app-specific content. Add a component
under `frontend/src/pages/`, then wire it into `frontend/src/App.tsx`
(extend the `nav` array and the active-route switch). Add API endpoints
to `frontend/src/api/client.ts`. See the
[`kit-dashboard` README](https://github.com/hollis-labs/design-kit/blob/main/packages/kit-dashboard/README.md)
for the `PageHeader` / `DataTable` / `SummaryCards` composition pattern.

## Dependencies

- **`@hollis-labs/design-app-runtime`, `@hollis-labs/design-components`,
  `@hollis-labs/design-tokens`, `@hollis-labs/kit-dashboard`** (`^0.1.0`) —
  transport/context, React components/AppShell, tokens, and dashboard chrome/theme.
  Consume published npm releases; update `frontend/package.json` and
  `frontend/package-lock.json` together, never a branch or git ref.
- **`github.com/hollis-labs/go-webui`** (`v0.1.0`) —
  the SPA-serving harness.

## License

MIT — see [LICENSE](./LICENSE).
