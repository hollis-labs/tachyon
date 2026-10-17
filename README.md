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
internal/localstore/    app-owned SQLite database, split pools + serialwrite queue
internal/apicache/      policy-driven durable API snapshots
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
Builder separates Definitions, Teams and Assignments; Agents remains the live
operations surface. Teams reads the authored `teams-policy.yaml` through a
fixed read-only proxy route, using the same browser/SQLite background-refresh
cache as other catalog reads. Its local workspace configures fresh singleton
seats, exact definition/launch bindings, roles, coordinator, history sharing
and spawn limits. These previews are not live memberships or proof of host
activation. Team drafts survive tab switches and background refresh, but not
leaving or reloading Builder. Demonstration saves leave them as unsaved drafts.

An explicitly imported snapshot can show existing saved team definitions while
API access is pending. `go run ./cmd/import-team-cache --database <tachyon.db>
--input <teams.json> --source <label>` imports a JSON array through the portfolio
SQLite writer queue into the existing bounded response cache. Builder lists
each saved version read-only, preserving the complete original definition and
showing the source/import time. It never labels this copy live. Refresh reads
the local snapshot without changing its timestamp or contacting Tether. The
running app gains no access to Tether's database; there is no HTTP import/write
endpoint. Imports expire under cache retention (seven days by default) and
may be evicted under its size limits; automatic sync still requires the read API.

Assignments stages agent-to-task/workstream links for local review and no-op
saves. Live assignments and target-ID validation are not connected. Persisted
team API listing, templates, audit and retirement-impact reads also remain future
work; the adapter never substitutes sample data when a read fails. Durable
object tables, history/links and merge/sync ownership are tracked separately
in CW-20261017-0025 through CW-20261017-0027, beyond the response cache.

The agent-os deployment is declared in `tachyon.cerberus.yaml`. Register that
file with Cerberus, then deploy `tachyon-backend-dev` to rebuild the frontend,
plugins and host together. It serves the existing `tachyon` systemd user service
on loopback port 8093 behind `https://tachyon.nanite.cloud/builder`. Host-specific
upstream settings belong in `~/.config/tachyon/env`; `TETHER_TOKEN_FILE` must
point to a Tether credential. Fragments Engine's API token is separate and does
not authenticate Tether catalog reads.

## Local database and API cache

Tachyon owns `$TACHYON_DATA_DIR/tachyon.db` (normally
`~/.local/share/tachyon/tachyon.db`). Portfolio `core/util/sqlite` supplies the
WAL reader/writer pools and bounded in-memory `serialwrite` queue; a snapshot
is acknowledged only after its disk transaction commits, with synchronous FULL.
The host drains accepted writes during shutdown. The database also holds cache
policy in `app_settings`, providing the foundation for the planned app-settings
and user-preferences modules. API credentials are not stored in the cache.

Safe JSON reads from Tether catalog, Torque activity/Planner/resources and
GitHub Forge use per-resource policies. Streams, mutations, message-inbox drains
and unknown routes bypass caching. Keys include the upstream and a hash of its
credential identity, plus request path and query. Successful mutations invalidate
snapshots for the affected upstream, preserving unrelated data; SSE-driven refetches and the Refresh button bypass freshness.

The browser keeps a bounded, transient navigation cache (128 responses / 16 MiB
of strings), with SQLite as the durable store. Reads show an existing snapshot
immediately, then revalidate in the background when its freshness expires.
An empty browser can request an older SQLite snapshot with
`Prefer: stale-while-revalidate`; it does not wait behind a pending GitHub read.
The background read uses `Cache-Control: no-cache`, replaces the snapshot, and
notifies mounted consumers through the portfolio app-runtime refresh registry.
These APIs return full snapshots, not deltas; replacement preserves removals and
server ordering. In-place refresh retains rows, selections and drafts. A cold
resource with no saved snapshot still waits for its first upstream response.

The shared page bar shows Live, Cached, Refreshing, Stale data or Connection error, with
the oldest successful update among that page's fetched snapshots. Agents also
shows its separate stream connection state. Live means recently fetched from
the source, not a promise that every source supports push updates. A stale
fallback retains the original timestamp and displays the remote error. An
observed 401/403 never falls back to saved data; configuration, unreachable
remote, timeout and access-denied states remain distinct. Known revoked access
is removed from the cache. Upstream access is rechecked when freshness expires
or on explicit refresh, not on each cached hit.

Tune policies by copying `config/cache-policy.example.json` to
`~/.config/tachyon/cache-policy.json` and setting `TACHYON_CACHE_POLICY_FILE` to
that absolute path in the service environment. Apply the host configuration
through Cerberus. Partial overrides preserve the other effective settings;
invalid/unknown knobs fail startup. Effective settings are saved in SQLite and
readable at `GET /api/cache/policy`. The general settings editor is tracked in
CW-20261017-0014; these knobs are usable through the config file now.

`fresh_seconds` avoids repeat upstream calls within its window;
`stale_while_revalidate_seconds` allows an older snapshot to render immediately
while the browser requests an authoritative refresh (zero opts out);
`stale_if_error_seconds` extends that window only for transient remote failures.
`timeout_seconds` bounds upstream waiting. `enabled: false` disables caching for
a resource. `max_entries`, `max_bytes` (payload bytes) and `retention_seconds`
bound retained snapshots, evicting oldest entries on writes. Expired snapshots
are never served beyond the policy window. SQLite may retain reusable file
pages after eviction; the payload limit is not a hard filesystem quota. Cache
misses sharing a key coalesce, and the queue provides write backpressure.

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
| `TACHYON_CACHE_POLICY_FILE` | Optional JSON override file; otherwise use persisted policy/defaults |
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


Tether session chat shares the existing chat drawer with mailbox conversations.
Chat launches come from Tether `/chat/agents` and appear in Agents with a chat
icon; ordinary process agents retain mailbox actions. The host uses its existing
Tether credential. Starting/continuing chat needs Tether's `operate` access;
authorization failures remain visible and keep the draft.

During this UI migration, `TANGENT_URL` (default `http://127.0.0.1:7843`) supplies
Agent Turns history and revision-checked replies. The bridge exposes only history,
participant-session establishment, and replies. Each browser has its own scoped,
HttpOnly Tangent participant cookie; its hash partitions the SQLite response
cache. Tangent retains custody of turns and is unchanged by this migration.
Before sending, Tachyon refreshes the roster and turns to distinguish a live
pending reply from a new turn/launch; stale-session turns cannot receive replies.

Tachyon's `chat_outbox` table stores initiated messages before delivery through
the same sqlitekit/serialwrite writer as the app cache. Successful sends replay
locally by key; unconfirmed sends reuse their key when retried within 24 hours.
The table survives response-cache eviction. This transport outbox is separate
from the planned general object/history model. Conversation copies/downloads
export the message body as Markdown; Info presents source and delivery metadata.
`chat` and `chat-turns` cache policies use the existing host knobs. Turn updates
are polled every five seconds; this does not claim token streaming.
