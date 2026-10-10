# Tachyon

Tachyon is a plugin-based control plane for the Hollis Labs agent fabric. A Go
host serves a same-origin API and an embedded React Sysop UI at `/sysop/`.
Local subprocess plugins expose validated capabilities; providers retain their
own agents, sessions, work, and service state.

## Layout

```text
cmd/tachyon/          HTTP routes, startup admission and staged shutdown
internal/plugins/    serial subprocess host, watchdog, restart and registry
internal/contract/   capability declarations and ok/error/ask envelopes
internal/hitl/       optional Tangent MCP bridge and read-only status
internal/observefeed/ private host activity DTOs
internal/webui/      embedded dist directory and go-webui handler
plugins/             eight module plugins plus the optional hello example
frontend/src/        App shell, pages/registry.ts and same-origin api clients
```

## Build and develop

Use the Go version declared in [go.mod](go.mod) (currently 1.26.6), and Node/npm
(the frontend CI uses Node 24). From the repository root:

```sh
make all                         # UI, all plugin binaries, then host
TACHYON_ADDR=127.0.0.1:8093 ./tachyon
# In another terminal, for hot reload:
make ui-dev                      # /sysop/, API proxy to loopback :8093
```

The listener defaults to `:8093` on all interfaces when `TACHYON_ADDR` is unset.
Use loopback for local operation; see [SECURITY.md](SECURITY.md). The UI is
embedded, but plugins are separate executables resolved under `./plugins/`
from the host's working directory. Running an installed host from an unrelated
working directory does not bring its plugins along.

| Command | Result |
| --- | --- |
| `make all` | Build UI, plugins, and host |
| `make build-plugins` | Build each `plugins/<id>/<id>` executable |
| `make ui-build` | `npm install` and Vite build into `internal/webui/dist` |
| `make build` / `make run` | Build host / build and run; reuse existing UI and plugins |
| `make install` | Build UI and plugins in this tree; `go install` the host to `$GOBIN` (Go's default if unset) |
| `make test` / `make vet` | Full Go tests / vet; full tests can need a live provider |
| `make clean` | Remove host, frontend dependencies and generated UI; retain `.gitkeep` and plugin binaries |

Before a UI build, go-webui serves a not-built placeholder. Keep
`internal/webui/dist/.gitkeep` so a clean checkout still compiles. Build outputs
and plugin executables are not committed.

## Plugins and provider boundaries

Startup discovers manifest-bearing plugin directories in sorted order and loads
an executable named after its directory. No per-plugin registration in main.go
is needed. The manifest-free `hello` example loads first when built.

| Plugin / module | Current provider and scope |
| --- | --- |
| `agent-ops` / `agent` | Nanite agents, grants and reflexes; skill/MCP catalogs and durable agents are read-only |
| `launch-ops` / `launch` | Prepared launches and Tether execution; execution/cancellation are open-world effects |
| `session-ops` / `session` | Tether sessions; creation, stop and submit are open-world effects |
| `work-ops` / `work` | Torque work items, assignment, transitions and comments |
| `service-ops` / `service` | Read-only Cerberus connector definitions, status and health |
| `scm-ops` / `scm` | Read-only local Git; no fetch, repository mutation or CI provider queries |
| `observe-ops` / `observe` | Ephemeral telemetry, dependency probes, Nanite snapshots and safe host activity |
| `config-ops` / `config` | Local declared settings; saved changes apply on plugin restart |

Declarations describe available verbs and `reads`, `writes`, `destroys`, or
`open_world` effects. Responses use `status: ok`, `error`, or `ask`. These labels
are not HTTP authentication or automatic permission enforcement. The host bounds
plugin I/O, retires broken subprocesses and exposes explicit restart recovery;
it never automatically retries an ambiguous plugin write.

See [host operations](docs/host-operations.md) for budgets, startup policy,
shutdown, retired registry entries and deployment; [ADR 001](docs/adr/001-host-contract.md)
for the contract; [HITL](docs/hitl-host.md) for optional Tangent correlation and
unavailable continuation; and [Observe feed](docs/observe-host-feed.md) for private
bounded telemetry and loss counters.

## Adding a page

Add the component under `frontend/src/pages/` and its route to
`frontend/src/pages/registry.ts` (`pageRegistry`). The shell consumes
`GET /api/nav`; plugin `capabilities.json` declares navigation and the required
verb. Visibility requires both supported capabilities and a registered page.
Keep API clients under `frontend/src/api/` and browser requests same-origin.
Read the declaration validation rules in ADR 001 before adding a plugin page.

### Navigation icons, reserved routes, and collisions

- **Icons**: Group icons use a documented bounded vocabulary corresponding to Lucide icons:
  `activity`, `clipboard-list` (or `clipboard`), `git-branch` (or `git`), `play` (or `rocket`),
  `server`, `settings`, `terminal`, `users`, `radio`, `dashboard`. Unknown icon names safely
  fall back to `activity` with host and browser diagnostic warnings.
- **Reserved routes**: Host routes (`/dashboard`, `/plugin-recovery`) cannot be claimed by plugins;
  `/settings` is host-reserved with an owner allowlist preserving the existing `config-ops` contributor.
  Unallowlisted plugin claims on reserved routes are dropped with a warning and diagnostic.
- **Collisions and attribution**: The host adds `plugin_id` attribution to all emitted groups
  and items. When multiple plugins declare the same group ID, item ID, or route, the first-loaded
  declaration wins. Dropped collisions are surfaced in `/api/nav` `diagnostics` and `notices`.

## Page guides

| Guide | Operator behavior |
| --- | --- |
| [Agent Operations](docs/pages/agent-ops.md) | Notifications, dialog focus and mobile rows |
| [Provider Sessions](docs/pages/sessions.md) | Provider history, metadata inspection and confirmed actions |
| [Work Board](docs/pages/work-board.md) | Per-status columns, filtered totals and explicit task actions |
| [Services and health](docs/pages/services.md) | Connector definitions, separate health snapshots and manual refresh |
| [SCM](docs/pages/scm.md) | Read-only repository status, activity and diffs |
| [Observe](docs/pages/observe.md) | Bounded snapshots, refresh and honest telemetry limits |
| [Settings and plugin recovery](docs/pages/settings-recovery.md) | Persisted settings and explicit restart recovery |
| [Operator decisions](docs/pages/hitl-ui.md) | Read-only decision tracking; continuation unavailable |

The [documentation index](docs/README.md) also links the host contracts and
operations guides.

## Dependencies and contributing

Go dependencies are pinned in go.mod, including plugin-sdk v0.4.0, go-webui
v0.1.0 and go-hitl v0.1.0. The frontend uses published design-kit packages:
design-components `^0.1.1`, runtime/tokens/dashboard `^0.1.0`, plus plugin-registry.
Update package.json and its lockfile together; use released packages rather than
git branches. See [CONTRIBUTING.md](CONTRIBUTING.md) for checks and review.

MIT — see [LICENSE](LICENSE).

### Published monorepo dependencies

Tachyon consumes `github.com/hollis-labs/substrate/mesh v0.1.0`,
`github.com/hollis-labs/libs/ui-go v0.1.0`, and
`github.com/hollis-labs/libs/plugin-mcp v0.1.1` with Go 1.26.8 or newer.
The bundled plugins and host now use protocol 2; rebuild the plugin executables
with the host. Protocol-1 executables refuse admission rather than falling back.

The manager issues a fresh generation for each spawn, validates Init before
starting the child, checks plugin identity and contract acknowledgement before
Load, and uses unique numeric IDs on its serial wire. It offers no reverse
services or hooks and sends an explicit empty reverse-grant set. Provider
endpoints and their existing application policy remain separate.

Persistent state keeps the existing `TACHYON_DATA_DIR`/XDG data layout.
Cache uses `TACHYON_CACHE_DIR` or the XDG cache layout; an explicit cache override
must be absolute. Writable roots must remain outside the plugin bundle, including
through symlinks. The browser consumes published registry v0.2.0 and the host
emits registry-v2 identities and revisions. No browser bundle is advertised for
plugins without a built bundle and integrity digest.
