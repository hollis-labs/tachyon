# Tachyon

A Go subprocess host serves `/api` and the embedded React UI at `/sysop/`.
Eight module plugins are discovered from the working directory; `hello` is an
optional legacy example. Providers own their external state. Read the authored
code and declarations before treating documentation or a running binary as the
current source revision.

## Start here

- [README.md](README.md): build, plugin inventory and page registration.
- [docs/host-operations.md](docs/host-operations.md): host budgets, admission,
  recovery, shutdown and Cerberus deployment.
- `cmd/tachyon/main.go`, `plugins_discover.go`, `shutdown.go`: HTTP routes,
  admission before listening, and HTTP-first shutdown.
- `internal/plugins/`: manager, watchdog, restart, settings and Observe worker.
- `internal/contract/`, [ADR 001](docs/adr/001-host-contract.md): validated
  capability/metadata declarations and result envelopes.
- `internal/hitl/`, [docs/hitl-host.md](docs/hitl-host.md): bounded Tangent bridge.
- `internal/observefeed/`, [docs/observe-host-feed.md](docs/observe-host-feed.md):
  safe private ingestion and loss accounting.
- `frontend/src/pages/registry.ts`: pageRegistry; App.tsx consumes `/api/nav`
  and capability gates. Browser API clients remain same-origin.

## Checks

Use the checks in CONTRIBUTING.md. CI runs Go vet/build/short tests and frontend
npm ci/typecheck/lint with error-on-warnings/build. Full `make test` can reach a
live Nanite; use `-short` with fake providers for isolated verification. Run race
tests for changes to host locks, lifecycle, queues or HITL correlation. Install
Lefthook once if using the tracked hooks; their presence alone installs nothing.

## Boundaries

- The plugin wire is serial: one callMu spans write/read and one
  long-lived decoder consumes stdout. Local JSON marshaling happens before I/O
  and lock acquisition. RPC IDs exist but do not multiplex calls.
  Never acquire another plugin's pipe while holding callMu or manager locks.
- Request cancellation governs queue waiting. Once I/O starts, a fresh host
  120-second ceiling applies; a browser disconnect cannot kill the plugin.
  Lifecycle-owner contexts can interrupt active I/O. Observe delivery has its
  separate 30-second queue and fresh two-second I/O budgets.
- Watchdogs close/kill only the captured process identity; delayed retirement
  cannot affect its replacement. Do not reuse damaged streams or retry writes
  whose completion is unknown. Recovery is explicit restart, including retired
  tombstones; a busy restart reports 503/unchanged.
- Present but invalid plugins refuse startup before a listener opens. Optional
  missing/unbuilt binaries and explicit discovery rejection have the documented
  different policy; do not turn malformed declarations into a silent fallback.
- Plugins are separate executables, not embedded. `make build` alone does not
  rebuild them or the UI. Preserve dist/.gitkeep and omit generated artifacts.
- config-ops is the sole writer of local settings.json. Declared values apply at
  spawn/restart. Plugin install/enable/disable is not implemented by this host.
- Skill/MCP catalogs and durable agents remain read-only; Tether sessions have
  their separately declared lifecycle verbs. Keep provider ownership upstream.
- Tangent status is read-only, fail-closed correlation. Approval cannot resume
  or redispatch a verb. Continuation remains unavailable; its design is tracked
  as a follow-up.
- Observe records safe identity/status/effect/duration only. Never include
  payloads, prompts, settings, credentials or upstream error bodies. The private
  ingestion command and marker cannot be exposed by HTTP or capabilities.
- HTTP has no authentication or built-in TLS. The code default is all-interface
  `:8093`; the reference deployment overrides it to loopback behind a private
  HTTPS proxy.
  Do not infer authorization from effects, same-origin checks or the proxy.
- Consume published design-kit packages and update the frontend lockfile with
  dependency changes. Keep page-specific docs pending the final documentation
  pass until their corresponding frontend changes are merged.

## Shared workspace and deployment

Use an isolated checkout/worktree and fake providers on an unused loopback port,
with temporary HOME/data, for tests. Do not build/run/clean a Cerberus-managed
live checkout during development. Deploying, reloading or altering the live
resource requires explicit operator authorization; a docs change does not grant
it. Compare source revision, built artifact and running service separately.
Open a PR against main for maintainer review; CONTRIBUTING.md carries the steps.
