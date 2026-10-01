# Tachyon

A headless-capable, plugin-based control plane UI for the Hollis Labs agent fabric. A Go binary serves a same-origin `/api` and an embedded React frontend (the Sysop UI). Capabilities come from plugins: subprocesses that speak the `plugin-sdk` protocol. Today the one real plugin is `agent-ops`, which fronts Nanite's HTTP API.

It is not: an agent runtime, a session host, or a store. Tachyon manages agents; it does not own sessions, skills' authoring, or MCP server registration. Those stay with the system that owns them (Nanite, Tether). If a change needs Tachyon to hold that state, it belongs upstream.

## Start Here

- `README.md` — what it is, dev loop, build.
- `cmd/tachyon/main.go` — the server: route table, the plugin proxy (`newPluginProxy`), graceful shutdown. `TACHYON_ADDR` overrides the listen address (default `:8093`).
- `internal/plugins/manager.go` — the plugin host: spawns plugin binaries, one JSON-RPC message at a time over stdin/stdout, builds the registry response the browser loader reads.
- `plugins/agent-ops/` — the Agent Ops plugin. `adapter.go` is the provider-neutral surface (including `AgentCapabilities`), `nanite_adapter.go` is the Nanite implementation, `plugin.yaml` is the manifest. `plugins/hello/` is a proof-of-concept plugin.
- `internal/webui/` — `//go:embed all:dist` plus the `go-webui` handler. The UI is served under `/sysop/`, not `/`.
- `frontend/` — Vite + React. `src/App.tsx` is the shell, `src/pages/` one file per screen, `src/api/` the same-origin client, `src/plugins/loader.tsx` the browser-side plugin loader.

## Commands

```sh
make run            # build + serve on :8093
make ui-dev         # Vite dev server, proxies /api to :8093
make all            # frontend build, plugin build, Go build
make test           # go test ./...
make vet            # go vet ./...
cd frontend && npm run typecheck && npm run lint
```

CI (`.github/workflows/ci.yml`) runs `go vet`, `go build` and `go test -short` (integration tests that need a live Nanite are skipped) on every push and pull request. Lefthook adds gofmt/goimports, `golangci-lint --new` and `go vet` on commit, biome on staged frontend files, and `go test` on push; run `lefthook install` once after cloning.

## Boundaries

- **The plugin wire is serial.** A plugin speaks one JSON-RPC message at a time over a single pipe pair with no request IDs, so `CallPlugin` holds `callMu` for the whole round trip, and stdout is read through one long-lived `json.Decoder`. Do not construct a decoder per call (it discards buffered bytes of the next response) and do not drop the lock (concurrent UI requests will interleave and corrupt each other).
- **Plugin binaries are not embedded.** They are spawned by relative path at runtime (`./plugins/<id>/<id>`), so `make build` alone ships a stale or missing plugin. Use `make all` or `make install`, which build plugins too.
- **`internal/webui/dist/.gitkeep` must stay.** `//go:embed all:dist` fails to compile when nothing matches. Do not delete it and do not commit the built bundle beside it; `make clean` keeps it.
- **The UI degrades by capability, not by assumption.** `GET /api/capabilities` reports what the active provider supports. A new UI action checks it first rather than assuming Nanite's shape is the only shape.
- **Read-only means read-only.** The skill catalog and MCP server catalog are read-only from Tachyon, and durable agents are read-only with no start/stop lifecycle. Adding a write path to either is a scope decision, not a refactor.
- **Pinned kit versions.** The frontend consumes `@hollis-labs/design-components` from npm at `^0.1.1`; `@hollis-labs/design-app-runtime`, `@hollis-labs/design-tokens` and `@hollis-labs/kit-dashboard` remain at `^0.1.0`. Bump versions through `frontend/package.json` and its lockfile, never a branch or git ref.
- **No authentication yet.** The HTTP API has no auth and the default listener is `:8093` on all interfaces. Treat it as a local tool. Do not add an endpoint that assumes a caller has been authenticated, and see `SECURITY.md` before changing how the server binds.

## Adding a page or a plugin

A page is a component under `frontend/src/pages/`, wired into the `nav` array and route switch in `App.tsx`, with its endpoints added to `frontend/src/api/client.ts`. A new backend capability is a plugin with a `plugin.yaml` manifest, registered in `cmd/tachyon/main.go` alongside `agent-ops`. Both keep the same-origin rule: the browser only talks to Tachyon, never to a provider directly.

## Contributing

Open a pull request against `main`; a maintainer will review it. `CONTRIBUTING.md` has the sequence.
