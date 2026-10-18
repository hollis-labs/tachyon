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
policy or deploys machines. Authored definitions describe configuration; session
and run records describe execution observations. Adapters normalize non-secret data instead of persisting raw credentials
or provider environment/config dumps.

Planner records are Tachyon-owned (CW-20261018-0017). Session/run records and chat
identity links are described below (CW-20261018-0018). Additional provider adapters
(CW-20261018-0016) and shared verb/API/MCP/CLI access with policy (CW-20261018-0019)
follow this foundation. The UI still reads its
existing adapters; this slice does not silently migrate cached responses or demo
drafts into master records. Revision retention, multi-source correlation, outbound
sync, and large-inventory staging need explicit later policies; no history purge
or upstream writeback is enabled here.

## Tachyon-owned plans and tasks

`objects.Plan` and `objects.Task` use the same UUID identity, ownership, CAS patch,
metadata, revision history and soft-delete contracts as the other domain objects.
They are authored work descriptions; setting `queued`, `doing` or `done` does not
dispatch work, execute a workflow, or transition a Torque task. General API/MCP
surfaces and the current Planner UI cutover remain separate work.

Both definitions embed `Common`: `name` is the title, `description` holds the body,
with `tags` and extensible `properties`. They carry required `status`, priority
0 (unspecified) through 5 (lowest; 1 is highest), optional `project_id`,
`acceptance_criteria`, and an RFC3339 `due_at`. Statuses are `draft`, `backlog`,
`todo`, `queued`, `doing`, `review`, `done`, `blocked`, `paused`, `archived`,
`abandoned`, and `cancelled`. Transitions are authored edits guarded by revision,
not an execution state machine. Plans may reference a `workflow_id`. Tasks may
reference a `plan_id`, a task `parent_id`, and up to 256 unique task IDs in
`depends_on`. Parent and dependency graphs must each be acyclic; self references,
unknown IDs and wrong-kind references fail atomically. Deleted records retain
their edges and history, and deleting a plan never cascades into its tasks.

Agents and teams connect through existing `Assignment` objects with a typed
`Target` pointing to a plan or task. This preserves assignment identity, state
and history instead of duplicating assignees on the task. Project membership is
explicit on each record; it is not inherited or rewritten when a plan changes.

The backend contract is `objects.Service`:

- `Create` / retry-safe `CreateWithID`, `Get`, revision-checked `Patch`,
  `SetDeleted`, `History`, and `Links` work for all planning kinds.
- `ListPlanner(PlannerFilter)` intersects optional kind, status, exact priority,
  project, plan, parent, epic, sprint, workstream, collection, assignee, strategic
  priority, typed target and promotion-state filters. It returns full `Record` values,
  ordered by UUID, with `AfterID` pagination (default 100, maximum 500). Deleted
  targets are hidden unless requested. `AssignedTo` finds agent/team IDs through
  non-deleted assignments of any state; callers can inspect the assignment's state
  through `Links`/`Get`. Empty kind lists all twelve Planner kinds. Relationship filters match explicit links even when the
  referenced container is deleted; only returned targets are hidden by default.
  Collection filtering here uses UUID order; ordered membership has its own read below.
- Queries use the indexed `planner_items` projection of the **effective**
  definition, updated in the same queued transaction as the object, links and
  revision. Provider observations cannot override local fields in that projection.

For example, using the host's existing object service:

```go
plan, err := service.Create(ctx, objects.Plan{
    Common: objects.Common{Name: "Build the control surface"}, Status: "draft",
}, nil, "operator")
// Handle err, then create a task under plan.ID.
task, err := service.Create(ctx, objects.Task{
    Common: objects.Common{Name: "Persist planning records"},
    Status: "todo", Priority: 2, PlanID: plan.ID,
}, nil, "operator")
// Handle err before using task.ID or task.Revision in a patch.
```

`planner.ImportTorque` takes a selected `TorqueRecord`, a credential-free instance
base URL, read-start timestamp, optional `objects.Correlation`, and actor. It is
a service operation, not a network client or background inventory job. Use Torque's
typed lowercase task representation: `kind=plan` seeds a local plan; `agent`,
`issue` and `task` seed local tasks. The first import creates a Tachyon master;
repeated imports update only its source snapshot/version and preserve local
content, links, metadata and deletion state. `objects.Service.ImportOwned` supplies
that atomic primitive. It refuses implicit promotion of an existing source-owned
record; use `TakeOwnership` explicitly first.

To associate an already authored record, supply its local UUID and expected
revision in `Correlation`. A source key can belong to only one object, and this
slice allows one source per object. A title match never establishes correlation.
External project/parent/dependency/epic/sprint/phase/collection IDs remain qualified
facts under `properties.torque`, with provider/instance on `Record.Source`; they
never become local foreign keys. Link known local UUIDs explicitly with `Patch`.
Later imports retain the latest external facts on `Source.Definition` without
rewriting those authored links. Environment, execution configuration, open remote
metadata, credentials, runtime phases and dispatch controls are not imported.
No existing API cache, Planner response, or Torque inventory is automatically
converted into a master record.

Migration `003_planner.sql` extends the object-kind constraint and adds the query
projection. Migration pins its SQLite connection, disables FK enforcement only
for transactional table replacement, checks all FKs before commit, and restores
enforcement before the writer queue and readers start. Existing IDs, source
snapshots, history and links are preserved. Migration `004_planner_organization.sql`
extends this projection and the kind constraints while preserving existing plans
and tasks. Migration `006_planner_records.sql` extends these constraints again
and adds immutable promotion receipts, preserving all existing projections.

## Epics, sprints, collections and workstreams

These four kinds share `Common`, optional local `project_id`, CRUD, revisions,
metadata and tombstones. All relationships use typed local UUIDs; neither matching
names nor provider IDs establish a link. Plans and tasks may explicitly reference
`epic_id`, `sprint_id`, and `workstream_id`. Epics, sprints and collections may
reference `workstream_id`. Membership is not inherited or cascaded on deletion.

- `Epic` adds `outcome`, priority 0–5 and optional `due_at`, with the same statuses
  as plans/tasks. Plans/tasks point to their epic; there is no duplicate member array.
- `Sprint` adds `goal` and optional `starts_at`/`ends_at`. Dates must both be absent
  or form a valid window with end after start. Statuses are `draft`, `planned`,
  `active`, `completed`, `cancelled`, and `archived`. Plans/tasks have one current
  sprint; revision history preserves moves between sprints.
- `Workstream` adds an optional local `workflow_id`. Statuses are `draft`, `active`,
  `paused`, `closed`, and `archived`. Closing is a status edit, not deletion.
- `Collection` has `active`/`archived` status and up to 1,000 ordered, unique
  `Reference` members of any supported kind except collections. An object may be
  in several collections. Unknown IDs, wrong kinds, duplicates and nested
  collections are rejected atomically. Typed `member` links retain ordinal slots.

`SetCollectionMembers(ctx, id, expectedRevision, members, actor)` replaces the
ordered list, supporting addition, removal and reordering with one CAS edit.
Null or empty members clears it. `ListCollectionMembers(ctx, id, expectedRevision,
offset, limit)` returns each member's ordinal, typed reference and current record,
including tombstones. Default page size is 100, maximum 500. Start at offset 0
with expected revision 0, then pass the returned revision and `NextOffset` to
continue. Each page reads a consistent database snapshot; collection edits cause
stale continuations to fail with `ErrConflict`. Member contents reflect each
page's read time. The collection's immutable history preserves earlier membership.

Assignments may target any of these kinds. Shared `ImportOwned` can seed normalized
organization definitions, while `planner.ImportTorque` still accepts only its
documented task/plan DTOs. These backend contracts do not change Planner UI data
sources or create upstream records.

## Owned captures, decisions, risks and strategy

Six additional kinds share the same object service, `Common`, metadata, typed
local `project_id`/`workstream_id`, history and tombstones. Their statuses are
editable descriptions, not execution commands or a mandatory transition graph.

| Kind / Go type | Fields beyond common context | Statuses |
| --- | --- | --- |
| `inbox_item` / `InboxItem` | numeric priority, origin, captured_at | new, triaged, archived |
| `idea` / `Idea` | numeric priority, hypothesis, expected_outcome | draft, exploring, accepted, rejected, archived |
| `decision` / `Decision` | context, options, outcome, rationale, decided_at, typed target | proposed, accepted, rejected, superseded, archived |
| `risk` / `Risk` | numeric priority, likelihood, impact, mitigation, contingency, due_at, typed target | open, mitigating, accepted, resolved, archived |
| `roadmap_item` / `RoadmapItem` | numeric priority, outcome, strategic_priority_id, plan_id, epic_id, starts_at, ends_at | draft, planned, active, completed, deferred, cancelled, archived |
| `strategic_priority` / `StrategicPriority` | numeric priority, outcome, success_criteria, starts_at, ends_at | draft, active, achieved, paused, archived |

Numeric priority retains 0 unspecified, 1 highest through 5 lowest; a strategic
priority is a separate objective record. Risk likelihood/impact use 0 unknown,
1 lowest through 5 highest. Roadmap/objective windows may be open-ended; if both
bounds exist, end must follow start. Optional dates must be valid RFC3339 values.
Options and success criteria cannot contain blank entries. Decision/risk targets
may reference any supported kind, checked against the local record. Assignments
and collections can reference all six new kinds. `ListPlanner` adds `TargetID`,
`StrategicPriorityID`, and optional `Promoted` filters using indexed links/receipts.

`Promote(ctx, PromotionRequest, actor)` supports inbox → idea/plan/task and
idea → plan/task. Each source has **one canonical promotion**; further breakdown
can create tasks under the resulting plan. The caller retains a target UUID and
source revision. Promotion copies name, description, tags, properties, numeric
priority, project/workstream links and local metadata into a new Tachyon-owned
record, then applies validated `Overrides` as a JSON merge patch. Default status
is `draft` for idea/plan, `todo` for task. Kind-specific source details stay on the
source and its history; provider identity is never copied. Source content and
status stay intact, while its revision advances with a `promote` history entry.

Target creation, both histories, immutable receipt and `promoted_to` /
`promoted_from` links commit in one queued transaction. `GetPromotion(sourceID)`
returns the original source/target revision anchors, actor and time. Identical
canonical requests replay without writes and return **current** records plus the
original receipt, even after later edits or deletion. A changed request conflicts;
retries cannot overwrite edits, resurrect a tombstone or create duplicate work.
Receipt-owned links survive subsequent patches and soft deletion. `Promoted`
filters distinguish processed captures without overloading authored status.
These are backend contracts; frontend cutover and public service/verb access are
separate slices. Existing cache/demo Planner data is not automatically imported.

## Sessions, runs and owned chat history

`objects.Session` and `objects.Run` share UUID identity, source observations,
local overlays, metadata, revision checks and retained history. Status edits never
start/stop work or send messages. `ExecutionLinks` provides optional typed local
project, workstream, agent, team, assignment, task and machine associations.

A session has lifecycle status, runtime family/provider, optional qualified
`native_session` reference, `run_id`, `parent_id`, `resumed_from_id`, lifecycle
timestamps (`opened_at`, `started_at`, `ended_at`) and exit code. Its source key
identifies the provider's session record, separately from the runtime-native
session ID. Unknown facts remain unset. Statuses are `unknown`, `created`, `ready`,
`launching`, `running`, `paused`, `detached`, `orphaned`, `completed`, `failed`, and
`killed`. Completed/failed/killed records retain history; status is not deletion.

A run represents an execution attempt, with optional parent/retry links and
start/end timestamps. Statuses are `unknown`, `planned`, `queued`, `running`,
`paused`, `blocked`, `completed`, `failed`, and `cancelled`. When `workflow_id` is
set, `workflow_revision` must identify an existing revision of that workflow.
Later definition edits do not move the run's pin. Retries use new run identities;
sessions link to their run, without a duplicated session membership list. Parent,
resume and retry lineage is checked for cycles inside the queued write transaction.
Run creation and correlation are explicit service operations; Tether session or
launch IDs do not automatically manufacture workflow executions.

`ListExecutions(ExecutionFilter)` intersects kind, status, project, workstream,
agent, task, run and workflow filters using the transactionally maintained
`execution_items` projection. Results use UUID keyset pagination, default 100 and
maximum 500. Deleted results are hidden unless requested; referenced tombstones
retain relationships.

Migration `005_execution_identities.sql` adds these kinds and separate
`chat_session_links`/`chat_outbox_session_links` tables. Host startup backfills
known session IDs in 200-row queued batches for the configured source and owned
outbox identity. It does not rewrite original message bodies, transport IDs,
revisions, reply intents or cursors. New chat ingestion saves the turn and its
identity link atomically. Successful outbox results with a known session get the
same association. Empty session IDs remain unlinked; current launch/workspace
state is never used to guess historical ownership.

Chat evidence can create a source-owned session stub with status `unknown`.
`EnsureSessionIdentityTx` only ensures identity: it cannot change an existing
observation, advance its freshness or resurrect a tombstone. New stubs use epoch
`observed_at` so an inventory read already in flight can enrich them. Later
session imports preserve that UUID. Inbox/outbox reads add `local_session_id`
without replacing `session_id`; the extra field is a read projection. Existing
reply authorization and transport identity checks still use original identifiers.
`Owned.SessionTurns` queries retained history by local UUID, scoped to the source,
including deleted sessions, with original turn-ID keyset pagination.

An offline, repeatable maintenance command supports DB-copy validation:

```sh
./tachyon backfill-session-links --data-dir /private/database-copy --tether-address https://tether.example
```

Use the original address stored on chat rows. Object keys normalize HTTP trailing
slashes and `tcp:` consistently with inventory ingestion, but original chat data
is preserved. Tangent browser-only history import/retirement remains separate.

## Provider ingestion and recurring jobs

`internal/objectsync` imports Tether projects, agent definitions, workstreams and sessions, Cerberus
machines, and GitHub organization repositories
through the durable object service. These are independent of response caches and
the current UI adapters. Tether project URNs and GitHub numeric repository IDs
retain local identity across renames. Local overrides, metadata, history and
ownership promotion retain the contracts above.

Tether reads `/registry/projects?status=*&include=external_ids` with the existing
paired token file. The current endpoint returns one unpaginated full inventory;
an unfamiliar envelope is rejected rather than assumed complete. Public authored
fields are normalized, with props and correlation facts under `properties.tether`.
Private callback, host and kind metadata are not requested. Deprecated/merged
projects become source tombstones; an active observation restores the same record.
Archived projects remain records with their upstream status. A successful complete
inventory can tombstone absent projects.

Tether agents come from the complete `/catalog/agents` inventory. Catalog IDs are
the source key; optional registry URNs are correlation facts, so assigning a URN
or changing a display name retains the local object. Renaming the catalog ID is
a source identity change. Disabled definitions remain records; only absence from
a successful full catalog scan tombstones them. `/registry/agents?status=*&include=external_ids`
supplies correlation facts; both reads must succeed before any changes apply.
Registry status does not override catalog presence. System instructions and agent
prompts remain separate. Declared provider/model, inheritance, MCP server IDs and
permission facts are preserved without importing environment variables, open
provider overrides, or connection credentials. These facts do not grant authority
in Tachyon, and inherited profiles are not compiled into launch configurations.

Explicit registry project references resolve through project URNs or Tether
external IDs in source observations from the same instance. Display names and
local property overrides do not establish identity. Missing or ambiguous targets
remain recorded as `project_ref` and `project_resolution` in `properties.tether`;
later scans can resolve them to local project IDs. Local relationship overrides
still win. Launch-specific worker/machine choices are not agent defaults. Teams,
launch/session placement, and broader correlation are separate follow-up slices.

Tether workstreams use stable IDs from unfiltered `GET /workstreams`. The current
endpoint is bounded to the newest 100 records and has no pagination; this source
is **non-authoritative** and never tombstones absent records. Run state records
`bounded_inventory_observed`, not complete inventory. Pagination remains upstream
work (`CW-20261018-0008`). Active and closed records are imported; closing/reopening
preserves identity and local edits. An unnamed workstream uses its ID as the name.
The provider's free-form `workflow_id` is retained as
`properties.tether.workflow_ref`, with provider creation/update timestamps.
It never becomes a local workflow foreign key automatically. Explicit local
project/workflow mappings and metadata survive later scans. Provider project
association and removal semantics remain Tether tasks `CW-20261018-0009`/`0010`.

Tether sessions read unfiltered `/sessions?limit=1000` and follow `next_cursor`.
All pages and relationship resolution must succeed before one atomic import.
Absence never deletes a session: the upstream timestamp-only cursor can skip
equal-time boundary rows, tracked in `CW-20261018-0048`. Creation/end timestamps,
state, provider, exit code and safe correlation references are retained; workspace,
PID, open environment/configuration and logs are excluded. Creation time is not
treated as execution start time. Native runtime identity and lineage remain unset
until explicitly supplied; upstream DTO extension is `CW-20261018-0049`.
Exact same-instance project, catalog-agent and workstream IDs/declared aliases can
resolve to local links; missing/ambiguous references retain their facts and resolve
on later scans. Local mappings win. Group/launch references remain opaque.

Cerberus reads its daemon's full, unfiltered `GET /hosts` inventory. Configure
`CERBERUS_ADDR=unix:/home/chrispian/.cerberus/cerberus.sock` for the local daemon, or
an HTTP base whose `/hosts` route exposes this same contract (an `/api` prefix is
allowed). `CERBERUS_TOKEN_FILE` is optional for authenticated HTTP endpoints.
These settings belong to the host, not plugin environments. Cerberus's declared
host name is its current API identity: hostname and address changes preserve the
local object, while renaming the declared key creates a new source identity.
Offline, unknown, and stale hosts remain records; only absence from a successful
full inventory tombstones them. Declared platform/architecture stay separate from
cached probe facts under `properties.cerberus`. Provider probe/check timestamps
and stale flags are retained; the import time is not a new measurement. No probe
is initiated. Reach credentials, token-file handles and raw probe errors are
excluded. Tether environment IDs remain opaque correlation references.

GitHub uses the configured organization and credential, follows all repository
pages on the fixed API origin, and imports archived repositories too. Its completed
inventory is **visibility-limited, not authoritative deletion evidence**. Missing
repositories, 404s, and permission changes do not delete local records. Confirmed
GitHub deletion events and broader identity/correlation policies are future work.
See [GitHub's organization repository API](https://docs.github.com/en/rest/repos/repos#list-organization-repositories)
for the upstream permissions and pagination contract.

Each scan starts fresh, gathers the source's available pages, then applies one atomic reconciliation.
Partial or failed reads apply nothing. Scans are bounded to 10,000 objects, 16 MiB
of normalized definitions, 8 MiB per HTTP response, and two minutes. Larger scans
fail visibly and need staging support. These client bounds do not establish
upstream completeness; the workstream endpoint's own bound is described above.
`object_sync_state` records the last successful import, phase, progress, diagnostic
checkpoint and sanitized failure; `object_sync_runs` retains attempt history.
Page checkpoints are diagnostic, not resumable cursors. A persisted three-minute
source lease fences concurrent manual/scheduled processes; expired runs are marked
interrupted before a fresh scan. A crash between object application and recording
success may repeat an idempotent import.

The host uses `core/util/scheduler` and its reference `sqlstore` for durable jobs,
fires, retries and restart recovery. Scheduler bookkeeping lives in private
`$TACHYON_DATA_DIR/jobs.db`, using `sqlitekit` with one writer connection, separate
from domain writes through the `tachyon.db` serialwrite queue. **No jobs are enabled
by starting the host.** Validate a source, then enable it through the narrow
maintenance command using the same upstream environment as the service:

```sh
./tachyon sync-objects --source tether-projects
./tachyon sync-objects --source tether-agents
./tachyon sync-objects --source tether-workstreams
./tachyon sync-objects --source tether-sessions
./tachyon sync-objects --source cerberus-machines
./tachyon sync-objects --source github-repositories
# Imports again; enables each source only if its own import succeeds.
./tachyon sync-objects --source all --enable --interval 5m
./tachyon sync-objects --status
./tachyon sync-objects --source github-repositories --disable
```

`--data-dir <directory>` permits isolated validation. Source names are
`tether-projects`, `tether-agents`, `tether-workstreams`, `tether-sessions`, `cerberus-machines`, `github-repositories`, or
`all`; commands exit nonzero on failure. `all` imports projects before agents;
independently scheduled jobs converge on later scans if a target is not yet present.
Credentials stay in the existing environment/token-file mechanism, never job
payloads. `--enable` also resumes a paused job after a successful import; an
existing job's interval must match. Interval editing is not exposed yet.
The first enabled fire is due after five seconds, then every five minutes by
default. Downtime coalesces missed scans into one; overlap is skipped, retries
are bounded to three attempts with exponential backoff, and successful fire IDs
deduplicate replay. Disabling prevents new dispatches (an already running scan
may finish). Shutdown cancels provider reads and drains the job engine before
closing either database. Job/scan history is retained; automatic pruning is not
enabled. These are host-owned jobs, not external Cerberus schedules.

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
