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

Builder also has **Projects** (`/builder?section=projects`) and **Workstreams**
(`/builder?section=workstreams`) areas backed by live Tether APIs. Projects edits
registry identity metadata (name, description, tags, guidelines, entry points),
shows external identifiers, and supports deprecation/restoration. It does not
edit the separate catalog launch-context configuration. Workstreams supports
create, edit, close/reopen, and session assignment, clearing, or lineage-aware
find-or-create. The list is limited to Tether's newest 100 matching records;
workflow and status filters are server-side, search is within the loaded set.
Project association and workstream deletion await Tether API support.

These areas perform real writes through the restricted Console
`/api/plugins/console/v1/tether/` adapter. Mutations require the existing
server-verified operator session; reads bypass response caching. The paired
Tether credential needs `read`, `maintain` for registry projects, and `operate`
for workstreams, and remains in the host's private token file. An uncertain
write is not automatically retried. The agent/team configuration previews
described below retain their demonstration-save behavior.

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
objects are implemented in `internal/objects`; connecting these forms and source
adapters remains separate work (CW-20261017-0030 / CW-20261018-0016).

## Durable objects

`internal/objects.Service` is the transport-independent data layer for agents,
teams/slots, assignments, workflow definitions, projects, machines and repositories.
Typed Go definitions share migrated `objects`, `object_sources`, `object_revisions`
and `object_links` tables in the existing `tachyon.db`. JSON definitions are
validated by kind; links have foreign keys and kind checks. Local `metadata` is
an extensible JSON object, separate from normalized provider data. This store
does not expire with response-cache retention. Existing chat and cache data remain
intact. Additive migrations and their ledger entries commit together at startup.

All mutations use the existing `sqlitekit` writer and `serialwrite` queue. The
service offers create/get/list, revision-checked patch/delete/restore, history,
incoming/outgoing links, source lookup/update-or-create, complete/partial inventory
reconciliation, and explicit ownership promotion. Retriable local creates use a
caller-retained UUID through `CreateWithID`; replay with different content conflicts.
Cancellation after enqueue can leave the result uncertain; read back the known ID
or source identity before retrying. List/history use bounded keyset pagination.

Source identity is `(provider, instance, collection, kind, external_id)`, never a
display name or repository URL. Each imported object has one authoritative source
in this slice. Different sources remain distinct until explicitly correlated;
cross-provider deduplication is not inferred. A source record combines its observed
definition with sparse local overrides; updates preserve those overrides and local
metadata. Patches merge JSON objects, replace arrays, and remove local keys on
`null`; removing an override reveals the latest source value. Nested `properties`
and repository `settings` permit extensions. Full source observations replace prior
observed fields rather than leaving vanished fields behind. Source version tokens
are opaque; scan-start timestamps order observations. Identical polls update the
observation time without creating artificial content revisions.

Explicit remote deletion retains a tombstone and history. Only a complete,
successful, unfiltered inventory may infer removal from absence. Partial results
never do; stale scans cannot undo newer observations or complete-scan tombstones.
Failed reconciliation rolls back the batch and watermark together. Local deletion
is separate and cannot be undone by sync. `TakeOwnership` records the explicit move
to a Tachyon master: future provider changes remain observations and cannot change
or delete the master. No operation here writes upstream, launches agents, applies
policy or deploys machines. Definitions describe configuration, not live runtime
state. Adapters must normalize non-secret data instead of persisting raw credentials
or provider environment/config dumps.

Planner will be Tachyon-owned (CW-20261018-0017). Chat session/run identities
(CW-20261018-0018), provider adapters (CW-20261018-0016), and shared verb/API/MCP/CLI
access with policy (CW-20261018-0019) follow this foundation. The UI still reads its
existing adapters; this slice does not silently migrate cached responses or demo
drafts into master records. Revision retention, multi-source correlation, outbound
sync, and large-inventory staging need explicit later policies; no history purge
or upstream writeback is enabled here.

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
`snapshot_first: true` (the Forge default) uses the full retention period for
immediate display and transient-error fallback instead of those two stale windows.
PR lists and details therefore open from saved data even after a long absence,
then refresh in the background once `fresh_seconds` expires. Completed responses
reconcile additions, changes and removals in place; GitHub reads here return full
snapshots rather than a delta feed. Authentication failures still discard saved data.
`timeout_seconds` bounds upstream waiting. `enabled: false` disables caching for
a resource. `max_entries`, `max_bytes` (payload bytes) and `retention_seconds`
bound retained snapshots, evicting oldest entries on writes. Expired snapshots
are never served beyond the policy window. SQLite may retain reusable file
pages after eviction; the payload limit is not a hard filesystem quota. Cache
misses sharing a key coalesce, and the queue provides write backpressure.

## Transcript insights

`/dashboards/transcripts` reads Fragments Engine's transcript API using the
host's private `FRAGMENTS_ENGINE_TOKEN_FILE`. `FRAGMENTS_ENGINE_URL` defaults to
`http://127.0.0.1:8091`; remote authenticated connections require HTTPS. The
credential stays on the server. This dashboard does not write to Fragments Engine.

Database-wide session, enrichment, project-path and tool/model counts come from
facets. Activity, usage, distributions and metadata search use a bounded snapshot
of the newest session metadata. `TACHYON_TRANSCRIPT_MAX_SESSIONS` defaults to 5000
(range 200–20000). Pagination uses a fixed start-time cutoff and deduplicates IDs;
it is not a transactional database snapshot. Undated records or concurrent
changes can make coverage differ from the facet total. The UI labels incomplete
coverage and keeps database totals separate from filtered calculations. Reported
tokens include cached tokens and are not a cost estimate. Search filters metadata,
not transcript bodies; session dialogs fetch redacted turns in pages of 50.

The existing browser/SQLite cache handles both metadata and turn pages under the
`transcripts` policy: 120 seconds fresh, 3600 seconds background revalidation,
86400 seconds stale-on-transient-error, and a 45-second upstream budget. Endpoint,
credential identity and snapshot limit partition its cache. Missing configuration
or denied access remains visible; observed authorization denials discard saved
data. This response cache does not change ownership of transcript records.

## Plugin inventory

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

Tachyon owns chat turns, operator replies, delivery observations and source
publication records in its local SQLite database. It ingests Tether channel
history with durable replay cursors, so it catches up after a restart without
replacing local resolutions or duplicating imported publications. Reply intent
and an idempotency key commit before the direct Tether reply request; acceptance
is displayed as queued until delivery is observed. Imported resolutions are
historical records and are never resent. The existing portfolio sqlitekit and
serialwrite writer owns these tables, separately from the evictable API cache.

`TACHYON_CHAT_SYNC_INTERVAL` tunes ingestion (default `5s`, allowed `1s`–`1h`).
The `chat-turns` cache policy controls the browser's fresh/revalidation windows;
local history remains readable with an explicit source problem during an outage.
Sending requires fresh session verification. The current deployment has one
operator; account-scoped ownership will join the planned identity module.

`go run ./cmd/import-chat-history --database <tachyon.db> --tangent-url <url>
--tether-address <url> --token-file <path> --sync` performs the explicit migration.
Back up the database first. It copies chat launches' turns and resolutions,
retains the original snapshot, and recognizes publications already ingested.
Tangent is not needed at runtime and its original records are left intact.
Messages initiated in Tangent that exist only in a user's browser localStorage
are not part of its server export; preserve/import those before retiring Tangent.

Tachyon's `chat_outbox` stores initiated messages before delivery. Successful
sends replay locally by key; unconfirmed sends reuse their key on explicit retry.
Chat prose uses the portfolio kit-chat Markdown renderer, with Builder-style
13px normal-weight text, restrained headings, lists, code, tables and safe links.
Raw HTML and remote images are not activated. Copy/download export the original
Markdown; Info presents source and delivery metadata. Updates are polled, not
claimed to be a token stream.

### Approvals

The **Approvals** page and chat drawer link open the same reusable review dialog.
It shows pending requests, retained decisions, source, impact, expiry, Markdown
and typed evidence. Approve/deny and attention acknowledgements (with optional
notes/replies) use the existing Tangent HITL v1 participant API. Producers still
submit to that durable queue and receive its immutable outcomes; Tachyon owns
this presentation, not a second decision authority. Tangent remains intact.

`TACHYON_APPROVAL_QUEUE_URL` defaults to `http://127.0.0.1:7843`; remote addresses
require HTTPS. Each browser has a separate HttpOnly participant cookie. The
host rejects cross-origin decision requests before relaying them, forwards no
ambient credentials, and partitions SQLite response snapshots by participant.
The `approvals` cache policy uses the same configurable freshness/revalidation
knobs as other APIs. Detail review and decision submission require fresh reads;
a changed, expired or already-ended request must be reviewed again. The UI
shows submitting state until the queue confirms an outcome. Evidence previews
bypass the response cache.

**Operator access** uses Tangent's `/api/session` and `/api/session/elevate`
authority. A shared modal asks for the existing operator key when a chat send,
reply or approval decision needs elevation; it can also be opened from Approvals.
Successful elevation rotates the browser's HttpOnly participant cookie and lasts
up to 12 hours, enforced by Tangent. Tachyon forwards the key only for verification
and clears the input on submission/cancellation; it does not persist the key.
Session status is never cached. Chat mutations check current assurance before
writing an outgoing intent; Tangent checks approval decisions itself. The cookie
is scoped to the Console API so both flows use the same browser session.

After explicit unlock, the client retries only an unambiguous pre-action
`assurance_insufficient` or `operator_session_required` refusal, once, with the
same body and idempotency key/reviewed revision. Scope denials, transport failures
and ambiguous outcomes are not retried. This temporary authorization dependency
on Tangent does not move Tachyon's chat history or outbox back to Tangent.

An approval records human intent for its requesting application. It does not
mint credentials, grant missing Tether scopes, or automatically replay a failed
operation. A plain chat report such as “missing session.write” is not an approval
request: the producer must submit the concrete request to the queue, and the
credential owner must handle any missing scope separately.
