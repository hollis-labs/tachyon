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
(CW-20261018-0016) build on this foundation. The Planner UI uses owned records;
scoped agent HTTP/MCP/CLI access is described under **Planner access for agents**.
The explicit portfolio migration preserved all seven Torque Planner databases;
cached responses and demo drafts are not implicitly promoted. Revision retention, multi-source correlation, outbound
sync, and large-inventory staging need explicit later policies; no history purge
or upstream writeback is enabled here.

## Shared local data verbs

`internal/dataverbs` is the transport-independent provider surface over
`objects.Service`. Provider selection is explicit (`tachyon`); verb names and
request/response DTOs do not depend on Tether, Torque or the UI. `Provider` exposes
`ID`, `Verbs` and `Invoke`; additional reviewed providers can be registered by the
host without implicit fallback or cross-provider identity guessing. The local
provider never invokes upstream APIs or launches work.

The host mounts this surface at `/api/plugins/console/v1/data`, inside the existing
operator cookie path. This is a **host-owned shared endpoint**, available to all
plugin UIs even when the console subprocess is unavailable. It bypasses the
response cache and returns `Cache-Control: no-store`. Existing provider proxies
and frontend consumers continue to use their current contracts.

- `GET /api/plugins/console/v1/data/providers` returns contract `version: 1`,
  providers, and each verb's name, `mutates`, description and `input_schema`.
  Schemas describe closed request shapes; domain validation also checks statuses,
  kinds, dates, references, revisions and other cross-field rules.
- Invoke a read with `GET /api/plugins/console/v1/data/providers/tachyon/verbs/{verb}`.
  Supply a single URL-encoded `input` JSON object; omission means `{}`. GET bodies
  and other query parameters are rejected. Read input is limited to 16 KiB.
- Invoke a write with `POST` to the same verb path and an `application/json` body,
  without query parameters. Every write requires same-origin browser access and
  a currently elevated operator session, checked against the approval service on
  every request. The host supplies audit actor `operator:browser`; request actors,
  identity headers and ambient bearer tokens do not grant authority.
- Read verbs are GET-only; writes are POST-only. Reads follow the host's existing
  read-access boundary and do not require operator elevation. This is not an
  external token-authenticated API. Agents use the separately authenticated
  `/api/agent/v1/data` or `/api/agent/v1/mcp` endpoints described below.

Successful calls return `{ "version": 1, "provider": "tachyon", "verb": "…",
"data": … }`. Errors use the host's flat `{ "code": "…", "message": "…" }`
envelope, backed by Hollis Labs `svcerr`: `invalid` (400), `not_found` (404),
`conflict` (409), `permission` (403), `internal` (500), `cancelled` (408), and
`timeout` (504). HTTP/assurance errors additionally retain `method_not_allowed`,
`unsupported_media_type`, `too_large`, `origin_denied`, `operator_session_required`
and `assurance_insufficient`. Internal database/transport details are not exposed.

| Verb | Input | Data result |
| --- | --- | --- |
| `objects.list` | kind?, include_deleted?, after_id?, limit? | records page |
| `objects.get` | id | Record, including tombstones |
| `objects.lookup_source` | provider, instance, collection, kind, external_id | Record |
| `objects.create` | id (retained UUID), kind, definition, metadata? | Record |
| `objects.patch` | id, expected_revision, definition?, metadata? | Record |
| `objects.delete` / `objects.restore` | id, expected_revision | Record |
| `objects.take_ownership` | id, expected_revision | Record |
| `objects.history` | id, after_revision?, limit? | items, next_after_revision? |
| `objects.links` | id, incoming? | items (current links, unpaginated) |
| `planner.list` | snake_case PlannerFilter fields | records page |
| `executions.list` | snake_case ExecutionFilter fields | records page |
| `collections.members.list` | id, expected_revision?, offset?, limit? | CollectionPage |
| `collections.members.set` | id, expected_revision, members | Record |
| `planner.promote` | source_id, expected_revision, target_id, target_kind, overrides? | PromotionResult |
| `planner.promotion.get` | id (source ID) | PromotionReceipt |

A records page contains `items` and optional `next_after_id`. History has the
analogous revision cursor. Page size defaults to 100, maximum 500; a full final
page can return a cursor whose next page is empty. These are live keyset queries,
not a multi-page snapshot. Ordered collection continuations additionally require
the first page's revision. An omitted `members` field is an error; explicit null
or `[]` clears membership. Unknown top-level fields, duplicate JSON keys,
trailing JSON values, and input deeper than 64 levels are rejected. Write input
is bounded to 3 MiB; existing per-definition and metadata limits still apply.

For example, create a local idea with a caller-retained ID:

```http
POST /api/plugins/console/v1/data/providers/tachyon/verbs/objects.create
Content-Type: application/json
Origin: https://tachyon.nanite.cloud

{"id":"019a0000-0000-7000-8000-000000000001","kind":"idea","definition":{"name":"Local orchestration","status":"draft","priority":2}}
```

The browser sends its existing operator cookie. Clients should retain a fresh UUID
for each create/promotion intent. If a response is lost or cancelled, read back that
ID or promotion receipt before retrying. Create retries with different current
content conflict; promotion retries compare the original request and cannot undo
later edits. General CAS mutations are not replay receipts: a retry after success
conflicts on the stale revision. No write retry should invent a new UUID or replace
the expected revision automatically. The frontend consumer cutover is a separate
step; no cache/demo records are promoted implicitly.

## Tachyon-owned plans and tasks

`objects.Plan` and `objects.Task` use the same UUID identity, ownership, CAS patch,
metadata, revision history and soft-delete contracts as the other domain objects.
They are authored work descriptions; setting `queued`, `doing` or `done` does not
dispatch work, execute a workflow, or transition a Torque task. Scoped agent
reads cover these models; agent writes currently cover the seven portfolio
databases. Torque remains the source for execution tasks.

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
These models are available through the shared local data verbs above. The owned
Planner frontend and scoped agent interfaces are implemented; see the migration
and agent-access sections below. Cache/demo data is not automatically imported.

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

Safe JSON reads from Tether catalog, the activity monitor, Torque resources and
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

## Activity monitor

Fleet activity reads the activity monitor directly via `TACHYON_ACTIVITY_URL`
(default `http://127.0.0.1:8099`). The Console snapshot route
`/api/plugins/console/v1/activity/agents` maps to
`/api/plugins/activity-monitor/agents`; its host stream
`/api/plugins/console/stream/activity` maps to `/api/stream`. Only `since`, `kind`
and `Last-Event-ID` reach the activity stream. No Torque/browser credentials are
forwarded. Cache identity includes the monitor URL. Configure a trusted monitor
endpoint; this transport does not yet support a monitor bearer credential.

## Portfolio Planner migration and local maintenance

`import-planner` captures all seven full Torque portfolio databases twice and
requires identical results before proceeding. It writes a new `0600` JSON
snapshot with source time and SHA-256. Use the host's `TORQUE_URL` and
`TORQUE_API_TOKEN` environment, and back up `tachyon.db` with SQLite backup before
applying. The default is a preview; `--data-dir` allows validation on a DB copy.

```sh
./tachyon import-planner --snapshot /private/torque-planner-preview.json
./tachyon import-planner --snapshot /private/torque-planner-import.json --apply
./tachyon local-data verbs
./tachyon local-data --input '{"limit":100}' planner.list
./tachyon local-data --input - objects.patch < /private/reviewed-patch.json
```

`local-data` invokes the shared verbs/services under local OS operator authority,
using the SQLite write queue and revision checks. It has full local object access;
it is not a scoped agent token. Flags precede the verb. Patch input uses `id`,
`expected_revision`, and a partial `definition` or `metadata`. On conflict, re-read
and reconcile rather than retrying a stale patch. `objects.create` requires a
caller-retained UUID for safe retries. External `data` API/MCP credentials have
explicit verb and kind grants; the original `planner-read` credential remains
read-only.

Imported identities use provider `torque`, the configured instance URL, collection
`planner/<database>`, and the original item ID. Priorities map to
`strategic_priority`, roadmap to `roadmap_item`, inbox to `inbox_item`; the other
databases map to singular kind names. Portfolio workstreams stay distinct from
Tether inventory workstreams. The complete original item—including comments,
options, links, source status and future fields—is retained at
`definition.properties.torque_planner.item`, with manual list position alongside.
Typed status mappings are explicit in `internal/planner/portfolio.go`; for
example, `landed` becomes `completed`, `closed` risk becomes `resolved`, and a
`decided` idea becomes `archived` without assuming acceptance. Source-only fields
such as rank and textual risk impact remain in the preserved item. External
references are preserved facts, not guessed local UUID relationships.

Refreshes update untouched masters. If both the source and local definition have
changed, the command reports a conflict; source changes cannot overwrite local
edits or revive tombstones. An unchanged source preserves local edits. Metadata
is always retained. Writes are atomic per record, not per snapshot; a mid-run
failure reports the affected item and can be retried safely. Absent upstream
records are never deleted locally. This is an explicit migration, not a recurring
two-way sync.

### Owned Planner API and frontend cutover

The host serves `/api/plugins/console/v1/planner` from owned records. The old
`/api/plugins/planner/v1/planner` prefix is a host alias, not a Torque proxy.
Planner's subprocess receives no Torque address/token. Schemas and the change
stream are local; only execution task enrichment contacts Torque, directly at
its task API. No runtime request needs Torque's Planner feature or settings.

The portfolio response is a flat item: `id` is the Tachyon UUID, `rev` its local
revision, `title` the authored name and `status` the native status. `legacy_id`
retains the imported ID. DB-specific GET routes also accept unambiguous legacy
IDs, so old links resolve. `/resolve?ref=...` returns `{db,id,item}` for either
form. Imported comments, options, ordering, external references and other details
remain visible; authored details live in `properties.portfolio`, separately from
the retained `properties.torque_planner` import. Native object edits remain
visible in the portfolio projection. Source-owned inventory workstreams are
excluded from the owned portfolio view.

All paths below are relative to `/api/plugins/console/v1/planner`. Reads return
plain JSON, not the generic verb envelope. Lists are full arrays; the underlying
service follows object pagination internally. HTTP and the
`planner.portfolio.*` verbs share the same service.

| Method/path | Input or result |
|---|---|
| `GET /databases` | Database counts and schema IDs |
| `GET /schema/:db` | Local fields and native status vocabulary |
| `GET /:db` | Full array; exact-match field filters and `sort`/`-sort` |
| `GET /:db/:id` | One item; UUID or unambiguous imported ID |
| `GET /search?q=...&db=...` | `{db,id,title,status}` hits |
| `GET /board?scope=curated\|all` | Owned board with direct task enrichment; enrichment failures appear in `notices` |
| `POST /:db` | `{id: retainedUUID, fields: {title, status?, ...}}` |
| `PATCH /:db/:id` | `{expected_revision, patch: {...}}`; null removes an authored field |
| `POST /:db/:id/comments` | `{expected_revision, text, kind?}`; author/time assigned by service |
| `POST /:db/:id/links` | `{expected_revision, link: {kind,ref,label?}}` |
| `DELETE /:db/:id/links` | `{expected_revision, kind, ref}` |
| `POST /:db/reorder` | `{items:[{id,expected_revision},...]}`; atomic, at most 500 |
| `POST` / `DELETE /relate` | `{from:{db,id},to:{db,id},expected_revision,field?}`; add/remove a resolved local reference |
| `POST /decisions/:id/decide` | `{expected_revision, option, comment?}` |
| `POST /decisions/:id/defer` | `{expected_revision, note?}`; proposed with `deferred:true` |
| `POST /decisions/:id/reopen` | `{expected_revision}` |
| `POST /inbox/:id/promote` | `{expected_revision,to_db,target_id:retainedUUID,fields?}`; atomic receipt and triage |
| `POST /inbox/:id/dismiss` | `{expected_revision,note?}`; archived |
| `GET /torque-titles?ids=...` | Up to 100 direct Torque task lookups |
| `GET /workstreams/:id/tasks` | Direct task API counts and newest 25 open tasks; complete scoped cursor scan |
| `GET /tasks/:id[/comments]` | Direct read-only Torque task detail/comments |
| `GET /events` | Local `planner.changed` SSE invalidations with revision cursor |

Writes require same-origin requests and the existing elevated operator session;
use the Console prefix because the operator cookie is scoped there. The legacy
prefix does not relax authorization or accept the old revision-less write
contract. Missing revisions are rejected. Conflicts return HTTP 409; item writes
include `details.current` when available. Re-read and reconcile instead of
retrying blindly. Create/promotion callers retain a UUID across retries.
Deletion/restoration remains available through the generic `objects.delete` /
`objects.restore` verbs with revision checks and retained history.

`/api/plugins/planner/stream/events` is also a local SSE alias. Its cursor tracks
persisted owned-object revisions, so CLI edits invalidate the browser too. It is
an invalidation stream, not a replay of every intermediate revision. Task events
remain available separately from the primary Torque task event stream through
`/api/plugins/console/stream/events`; subscribe to task events there when updating
execution counts. No Torque `planner.changed` event is needed.

The frontend uses the owned client by default, with native statuses, UUIDs,
guarded mutations, conflict handling and separate Planner/task event sources.
There is no build flag or fallback to Torque Planner. A newer revision prompts
an explicit reload while preserving the current draft. The development proxy
preserves the browser-facing Host so same-origin operator writes work there too.

Retire Torque's old Planner feature only after coordinating its remaining
writers and reconciling final changes; its execution-task APIs remain the primary
source for execution data.

## Plugin inventory

Builder's Teams editor saves authored definitions through the guarded Tachyon
`objects.create` / `objects.patch` verbs. The object UUID is retained across
create retries; edits, retirement and restoration use the reviewed revision.
Native slots/coordinator remain queryable, while `properties.builder_team`
retains the editor draft and complete pinned provider preview. The preview is
desired configuration, not evidence of provider acceptance or a running team.
Saving never registers, launches or changes a team in Tether.

Saved teams use 50-record cursor pages, an in-memory page cache and background
refresh. Details load when selected; open drafts keep their original revision
until explicitly reloaded. Conflicts and failed saves retain the draft. Imported
Tether snapshots remain read-only; definitions authored in other formats are
retained without forcing them through this editor.

Builder's Agents and Assignments areas edit native owned records through the
same guarded service. They load independently of Tether's catalog, retain drafts
between Builder tabs, fetch 50-record pages, display cached pages while refreshing,
and fetch details/history only on demand. Saves send only the reviewed field
deltas, so unexposed fields and metadata survive. Provider observations require
an explicit adoption action before editing; adoption retains source provenance.
Retirement/restoration retains UUIDs and history. History uses 25-revision pages.

Assignment assignees are local agent/team UUIDs. Work targets can be local records
or explicit provider/instance/collection/external-ID references; external IDs are
never guessed to be local records. These are desired assignments, not dispatched
work. Agent enabled flags likewise describe desired configuration, not runtime
state. Deployment and runtime orchestration remain separate provider operations.
The Provider catalog area retains the earlier read/preview interface; its
demonstrations do not write to Tether.

| Plugin | Backend | UI routes |
|---|---|---|
| `planner` | host-owned Planner API and local change stream; direct primary task enrichment | `/planner`, `/planner/:db`, `/planner/:db/:id` |
| `console` | forwards `/api/plugins/console/v1/*` to Torque `/api/v1/*` | `/agents`, `/builder`, `/prs/inbox`, `/pr/...` plus a shell overlay |

Request flow:

- **Buffered calls:** the browser calls `/api/plugins/{id}/...`. app-host passes these to the plugin process over `http/handle`, and the plugin calls the upstream.
- **Server-sent events:** the app-host plugin path buffers responses. The host therefore relays declared streams at `/api/plugins/{id}/stream/{name}` directly to the upstream.

Phase 1 compiles plugin UIs into the host frontend. Runtime-loaded bundles are tracked by CW-20261016-0165.

### Reviewed team formation

Saved native Builder teams can explicitly form a team in Tether. The first
provider action surface uses `tether` data verbs `teams.preview`, `teams.apply`,
`actions.get` and `actions.list` under the existing same-origin data API.
Both preview and apply require live operator assurance. Preview is a local write
only: it reads the saved team revision, validates its pinned Builder payload,
adds the reviewed mission to each slot, and retains the exact request. Each
formation uses `tachyon-{object UUID}-{action UUID}` as its provider definition
ID: Tether definitions are immutable by ID/version, while missions may differ
between runs. The authored team ID and saved definition remain unchanged. It does
not claim that Tether has admitted the launch. Apply sends `POST /teams/form`;
Tether remains authoritative for definition resolution, caller scopes, trust,
activation policy and runtime admission.

The first mapping supports fresh singleton seats with SHA-256 definition pins,
at least one required worker, and an optional coordinator seat occupied by the
authenticated Tether caller. Native `agent_id` slots select an explicit
`launch_binding_id`; formation resolves their exact definition pins from the
confirmed binding. Slots without local agents keep their authored provider pins.

`provider_actions` retains immutable payloads, revision and digest, actor,
provider target, a private credential fingerprint, and outcome receipts using
the shared SQLite writer. Initial apply checks ownership, retirement and revision
atomically with claiming the request. Unused previews expire after 30 minutes.
Each action has a retained UUID and Tether idempotency key. Repeated successful
apply reads its receipt; parallel apply is refused while a two-minute lease is
held. Upstream calls are bounded to 40 seconds, while Tether can continue the
operation after a disconnect. A timeout, failure or malformed receipt remains
`unknown`; refresh is read-only and recovery requires an explicit retry of the
same request. After a host crash, the same-key retry is available when the lease
expires. Credential or target changes refuse replay under a different caller.

History loads 25 compact summaries per page, newest first, with a retained UUID
cursor. Payload and result details load on selection. A receipt describes a
provider response at a point in time; it is not current runtime health. Formation
neither changes the saved team nor marks planned assignments executed.

Runtime controls use `runtime.preview` and `actions.apply` for assignment dispatch,
team messaging, adding/removing members, cancellation and dissolution. They select
an explicit succeeded formation receipt. `teams.roster` returns saved membership
first; `refresh: true` performs a separate provider read and retains the cache on
failure. Catalog publication uses `agents.publish_preview`, `catalog.apply_preview`
and `actions.apply`. Tether owns approval of its exact plan and digest. Unlike
team operations, catalog POSTs have no durable idempotency contract here: an
unknown installation uses `actions.reconcile` readback, never a blind POST replay.

No production launches were used to validate these integrations. Connectivity and
live acceptance are tracked separately: CW-20261019-0004 covers the paired device
identity rejected by Tether's team APIs; CW-20261016-0248 covers remote catalog
write/approval authority. Effective tool/MCP grants, provider activation evidence,
placement overrides and external run adoption remain separate integration work.

### Owned-agent launch bindings

An owned agent revision can publish an immutable agentdef-v2 file and bind to a
specific provider launch. These backend verbs live under the same `tether` data
provider and use the existing operator authorization boundary:

| Verb | Method / inputs | Result |
|---|---|---|
| `agents.definition_preview` | POST: action `id`, `object_id`, `expected_revision`, `options` | retained catalog-plan preview with `definition_artifact` |
| `bindings.preview` | POST: binding `id`, `agent_id`, `expected_revision`, `launch_id`, `options` | reviewed catalog evidence; performs GETs only upstream |
| `bindings.confirm` | POST: binding `id`, reviewed `digest` | locally active binding |
| `bindings.revoke` | POST: binding `id` | revoked binding with retained history |
| `bindings.get` | GET: binding `id` | retained evidence, no provider call |
| `bindings.list` | GET: `agent_id`, optional `before_id` | 25 bindings per page, newest first |

`options` requires `permission_profile`, an explicit `tool_capabilities` map for
all local tool declarations, and `skills` entries `{name, content: {uri, digest}}`
for every declared skill. Skill digests cover complete packages. These are
requirements and a named harness profile, not effective grants; provider resolve
must verify dependencies and enforce host policy. This slice emits ephemeral
agent definitions for fresh singleton team seats. Agent instructions become the
Markdown body; description (or name when absent) supplies purpose. Runtime/model
are launch selections, and local roles remain team configuration.

Publication creates one versioned file under
`team-definitions/tachyon-<agent UUID>/r<agent revision>-<semantic SHA256>.md`.
It uses the shared `libs/mesh/core/agentdef` parser/digest contract and keeps
semantic and exact artifact digests separate. Changing publication options
produces a different revision/path. `definition_artifact` in the retained action
contains the exact file and definition pin, reconstructed from saved bytes.
Apply the preview with `actions.apply`, then use `catalog.apply_preview` and
`actions.apply` for provider-approved installation. Unknown catalog outcomes
retain the existing readback/reconciliation rules; publication never creates a
launch allowlist entry or bypasses Tether approval.

Once the provider has configured the launch entry, `bindings.preview` reads the
expected definition file and `teams-policy.yaml` through `/catalog/files`. It
validates the actual file with agentdef and requires the exact expected ID,
revision and semantic digest, a matching `catalog:` source, and the selected
launch runtime/model matching any values authored on the agent. Local project
or machine placement currently refuses binding because the team API does not
carry those overrides. An unspecified placement remains the provider launch's
default; it is not a claim that execution happens on the hub.

A binding freezes the agent UUID/revision, provider target and private caller
fingerprint, selected launch, definition pin, artifact and policy hashes, and
reviewed options. Confirmation requires its digest, an unchanged active/enabled
owned agent, the same provider caller and a preview younger than 30 minutes.
`active` means locally confirmed, not provider admission: evidence explicitly
reports `activation: catalog_observed_runtime_unverified`. Tether currently
starts the catalog launch associated with a trusted pin; catalog file inspection
alone does not prove that the daemon loaded that policy or that the process uses
the definition body and permission profile. Provider activation/execution
attestation is a separate Tether integration requirement.

Save both `agent_id` and `launch_binding_id` on each owned-agent team slot using
normal team CRUD. The Builder preview still provides topology, routing and team
authority; its pin for that slot is replaced by the explicit binding before
validation. `teams.preview` retains `binding_dependencies` alongside the exact
provider payload. Prepare and initial apply recheck binding state and agent
revision through the shared SQLite writer. Edits, disablement, retirement,
revocation or caller changes require another reviewed binding. `member_add`
checks the originating slot binding too. Revocation does not stop existing
processes or prevent cleanup; explicit recovery of an already uncertain request
keeps its original payload and idempotency key even after local edits/revocation.
Binding lookup never selects a newest version implicitly.

Bindings and revocations persist in migration 011; old formation receipts have
empty dependencies and retain their original behavior. Frontend binding controls
are a follow-on consumer of these verbs. External agent-token write scopes remain
unchanged. Validation uses fixtures; live provider writes and acceptance remain
deferred.

### Provider execution tracking

Confirmed team formations also produce ordinary Tachyon `run` objects. The source
identity is `(tether, configured instance, teams/runs, provider run ID)`, with a
local UUID and a link to the exact formation receipt and reviewed team revision.
The team revision supplies the name and project association even if the saved
team is edited later. Provider observations use the normal object merge/history
service: local overlays, metadata, adoption and tombstones survive refresh.

The receipt and run projection share the SQLite writer transaction. If projection
fails, a savepoint rolls back partial object writes while retaining the confirmed
provider result and a separate `projection_problem`. `execution_run_id` in action
receipts identifies the local run when available. Repair does not resend an action
or need a working credential; it reconstructs the link from the stored formation
and latest retained roster. Old installations can backfill a formation the same
way. GETs never repair or contact Tether.

The `tether` provider exposes these additional verbs through the same-origin data
API (`/api/plugins/console/v1/data/providers/tether/verbs/<verb>`):

| Verb | Method / inputs | Result |
|---|---|---|
| `runtime.runs` | GET: optional `team_id`, `assignment_id`, `include_deleted`, `before_id`, `limit` | compact run page with `next_before_id` |
| `runtime.activity` | GET: local `run_id`; optional `assignment_id`, `before_id`, `limit` | action summary page including prepared, pending and unknown outcomes |
| `executions.project` | operator POST: retained action `id` | receipt with repaired `execution_run_id` |

Read inputs are JSON encoded in the `input` query parameter. Pages default to 25
and cap at 100; the returned cursor is opaque to consumers. Run pages order by
formation confirmation time; activity pages order by action creation time. Full
objects/history remain accessible through Tachyon's `objects.get`,
`objects.history` and `executions.list`; full action details use `actions.get`.
Run summaries distinguish effective `status` from raw provider `reported_status`
and include `observed_at`. A reported `working` run maps to `running`, but that
observation never proves current process health. Receipt times do not become
process start/end timestamps. Unknown provider states map to `unknown`.

Assignment-filtered run lists include attempted dispatches (including uncertain
ones), not unused previews. Inspect `runtime.activity` for the outcome of each
attempt. One run may have many assignments; no single assignment is invented as
its owner, and dispatch never edits assignment planning status. Cancellation or
dissolution receipts alone do not infer a terminal run status: only a roster
observation updates it. Member actor strings are not session IDs, so this slice
does not synthesize session records or session-to-run links. External agent-token
scopes are unchanged; these provider verbs do not become agent-authorized writes.

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
| `TACHYON_ACTIVITY_URL` | `http://127.0.0.1:8099` (activity-monitor host) |
| `TACHYON_PLUGIN_DIR` | `bin/plugins` |
| `TETHER_ADDR` | Tether catalog endpoint: HTTPS URL, loopback HTTP/TCP, or `unix:/path`; unset shows a configuration error in Builder |
| `TETHER_TOKEN_FILE` | Optional private `0600` bearer-token file, read by the Console backend; never sent to the browser |
| `TESSERACT_URL` | Tesseract HTTP base (loopback or HTTPS); unset returns 503 from `/api/plugins/console/v1/tesseract/*` |
| `TESSERACT_TOKEN_FILE` | Private `0600` file holding a Tesseract token with `tesseract.read` only; never sent to the browser |
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

The **Approvals** page uses Tachyon's own SQLite HITL store and shared
`libs/mesh/core/hitl` service (v0.8.0). Requests, revision checks, idempotency keys
and immutable outcomes survive restarts. The shared SQLite write queue serializes
transitions; concurrent decisions have one winner. Existing Tangent requests stay
in Tangent to drain; no items or decisions are copied between queues.

The browser API remains `/api/plugins/console/v1/approval-inbox`. Inbox reads are
uncached authoritative local reads, with 25 items per pending/history page
(maximum 100 via `limit`). `next_pending` / `next_history` are opaque item cursors
passed as `pending_before` / `history_before`. Search applies to the loaded page.
The UI retains the displayed page on refresh failures, polls while visible, and
rechecks the exact reviewed revision before recording a decision. Inline evidence
and HTTPS provider links are supported; remote evidence is not fetched through
Tangent. Legacy `approvals` cache settings no longer affect the native queue.

Low-class requests require an explicit browser decision. Medium-class requests
also require current operator elevation. High-class requests are provider-console
handoffs only; Tachyon cannot resolve them. Cerberus is handoff-only until its
proof verifier lands (CW-20261019-0014); its requests must use class `high`.
A handoff link opening is not a decision and never marks an item approved.

**Operator access** still uses Tangent's `/api/session`, `/api/session/elevate` and `/api/session/de-elevate`.
`TACHYON_APPROVAL_QUEUE_URL` defaults to `http://127.0.0.1:7843` and now selects
only this session authority; remote addresses require HTTPS. Each browser has an
HttpOnly participant cookie. Current assurance is checked on every inbox access;
decisions require the Tachyon origin. Elevation rotates the cookie. The operator key
is forwarded for verification and never retained.

**Elevation lifetime** is an operator setting: `always` (default; until revoked, still
bounded by Tangent's 30-day browser session), `never` (not remembered; about one
minute) or `<N>d` (1–365 days). It is stored in `app_settings`
(`approvals.elevation_ttl`) and managed with `tachyon elevation-ttl [get | env | set
VALUE]` or `GET`/`PUT /api/plugins/console/v1/settings/elevation-ttl` (PUT needs operator elevation; the route sits under the operator cookie's path so the browser sends it).
**Tangent enforces it**, not Tachyon: Tachyon has no timer of its own and only mirrors
`elevation_ttl` / `elevation_expires_at` from Tangent's `/api/session` for its copy.
Launch Tangent with `TANGENT_ELEVATION_TTL` set to the value printed by
`tachyon elevation-ttl env`; a change takes effect when Tangent restarts (Tangent has
no runtime setting call). Shortening applies at once on restart, lengthening applies to
new elevations only, and a lapsed elevation never revives. **Revoke elevation** (the
Approvals page, `POST …/approval-inbox/de-elevate`) calls Tangent's
`/api/session/de-elevate` and adopts the replacement session cookie.
Native decisions record the server-derived session reference and assurance;
request bodies cannot supply participant metadata or proof. Bearer credentials
are not browser decision authority. Session custody migration remains separate.

After explicit unlock, the client retries only an unambiguous pre-action
`assurance_insufficient` or `operator_session_required` refusal, once, with the
same reviewed revision. Transport failures and ambiguous outcomes require lookup.

#### Producer API

`POST /api/approvals/v1/{enqueue,get,await,withdraw}` accepts the corresponding
shared go-hitl v1 wire payloads. Producers use an **existing Tether-issued Ed25519
JWT**, audience `tachyon`, pinned issuer, and exact configured subject. There is
no new key issuer, pairing flow or enrollment in this slice. Unconfigured producer
trust returns 503; invalid configuration fails startup. Existing Planner API
credentials and paired-device tokens are not interchangeable with these JWTs.

Set `TACHYON_APPROVAL_PRODUCER_CONFIG` to a host-managed JSON file. For example,
replace the placeholder issuer, JWKS URL, subject and origin with verified
existing deployment values before installing:

```json
{
  "issuer": "<existing Tether issuer>",
  "audience": "tachyon",
  "jwks_url": "https://<Tether host>/.well-known/jwks.json",
  "deny_list_url": "https://<Tether host>/auth/denylist",
  "producers": {
    "<existing Cerberus JWT subject>": {
      "application_id": "cerberus",
      "minimum_class": "high",
      "handoff_origins": ["https://<Cerberus console host>"]
    }
  }
}
```

An optional `bootstrap_key_file` accepts public JWKS or an Ed25519 public PEM.

`deny_list_url` (Tether's public `/auth/denylist`, same URL rules as `jwks_url`) makes producer tokens **revocable**: Tachyon polls the list every 30 seconds and rejects a token whose `jti` or subject is on it, within one poll interval after a successful poll. If the endpoint becomes unreachable the last list stays in force (stale), so a revoked token stays rejected through an outage. While it is configured the producer API answers 503 `producer_revocation_unavailable` until the first successful poll, because until then Tachyon cannot know what has been revoked. Configure it for any producer that holds a long-lived `kind=service` token (a very distant `exp`): without it such a token can only be ended by rotating the issuer key or removing the subject mapping and restarting. **Release posture:** `deny_list_url` is REQUIRED whenever service tokens are admitted to the producer API; the optional, no-list configuration is a dev-posture allowance only. While the first poll has not succeeded Tachyon logs a WARN naming only the deny-list host (never the URL path, userinfo or query, and no poll error text) on every retry, then an INFO when it becomes ready.

**Reload.** The producer config file is re-read when its content changes (checked every 2s, applied after two identical checks) and on `SIGHUP`, without a restart. The whole config — issuer, audience, JWKS, deny list and subject mapping — is one generation: a request is verified and authorized against the single generation it started with, so a reload landing mid-request cannot mix an old verifier with a new mapping. A reload is all-or-nothing: an invalid file, an unreachable JWKS (when there is no `bootstrap_key_file`) or an unreachable *new* `deny_list_url` is logged and the last good config keeps serving. While `deny_list_url` is unchanged the same list and poller carry over, so a reload never forgets a revocation, even during an outage. Changing or removing `deny_list_url` is an explicit act; a changed URL must answer one poll before it is adopted. Reload cannot enable or disable the producer API; that is a restart. A producer reload changes trust only (issuer, audience, JWKS, deny list, subject mapping); it has no auth mode, so there is nothing for it to loosen, and the loopback-only reload-loosening rule of `security/auth` v0.3.0 (`ReloadBind`) does not apply to it. Tighter trust (a narrower mapping, a deny-list URL added) and looser trust (a wider mapping, a removed `deny_list_url`) are both the owner's explicit edit of the producer config, logged by the reloader; the release-posture rule for owner edits (CW-20261020-0037) is where any constraint on that belongs.
Subject mappings bind each producer to a distinct application ID and minimum
class. `source.application_id` and `caller.application_id` must match that
binding; supplied `caller.principal_ref` must match the authenticated subject.
Agent IDs remain producer-asserted display metadata. Cross-producer lookups return
404 and expose no other producer's records. Scopes use Tether's `app.verb` grammar (underscores within the verb); the earlier
three-segment `tachyon.approvals.enqueue` spelling is not issuable. Tokens need
only the operations used:

| Operation | Required scope |
|---|---|
| enqueue | `tachyon.approvals_enqueue` |
| get, await | `tachyon.approvals_read` |
| withdraw | `tachyon.approvals_withdraw` |

Enqueue uses `contract_version: "1.0"`, `kind: "approval"` or `"attention"`, a
stable `idempotency_key`, and `source` with `application_id` and `agent_id`.
Required extras are `title`, `summary`, `request`, `class` (`low`, `medium`, `high`)
and `op_digest` (`sha256:` plus 64 lowercase hex digits). `handoff_url` is required
for high and must use a configured exact HTTPS origin. Set `expires_at` for
operations that must time out. Extras are retained in the immutable request
snapshot and covered by shared HITL idempotency; changed content with the same
key conflicts rather than replacing the reviewed operation.

Get/await/withdraw use `contract_version`, `item_id` from the returned handle, and
`caller: {"application_id": "..."}`. Await accepts `wait_ms` (up to 50000); a
waiting timeout does not cancel the request. Logical expiry applies on reads;
late writes cannot approve expired requests. Await is also bounded by JWT expiry.
There is deliberately no producer resolve/approve endpoint.

After a terminal notification or retry, producers must **look up the authoritative
outcome** and independently verify their expected digest, operation revision,
expiry and current execution authorization before acting. Tachyon validates the
digest format and preserves it; it does not verify a canonical operation or a
digest-bound cryptographic proof. Approval grants no scopes, mints no credential,
and does not trigger execution. Cerberus's own passkey ceremony remains the
authority for its protected operations. Live producer acceptance requires existing
JWT trust/identity configuration and supported Tether enrollment. The shared-proof
owner hold was lifted; production enrollment still depends on its implementations.

#### Registered actions (approve and run)

`internal/actionrun` runs an approved action on a chosen machine and records the
result against the approval. It is not a shell: the registry (`actionrun.Actions`)
holds fixed argv templates with typed, pattern-checked parameters, and a request
names a **machine id** (`agent-os` runs locally, `hub` runs over ssh), an **action
id** and parameters. Day one: `cerberus.resource.{apply,deploy,reload,remove,stop,sync}`
(`--ack` is part of the template) and `tether.catalog.approve` (hub only).

- **Digest.** The server computes `op_digest` = sha256 of the canonical machine,
  action, params and argv when the request is made; the approval item carries it.
  Before starting, the executor re-resolves the stored request from the registry and
  refuses (`refused`) unless the digest equals both the stored and the approved one.
- **Environment class.** For Cerberus actions the target resource's own labels are
  read on the target machine (`cerberus resource list -o json`). `env=dev owner=self
  admin=self` becomes a class-`medium` approval (operator elevation required);
  prod, `admin=owner`, unlabeled or unknown targets are refused at request time
  because they need the Cerberus passkey path. The labels are read again when the run
  starts (approval can wait up to 30 minutes); a target relabelled non-dev since, or whose
  labels cannot be read, is `refused` (fail closed).
- **Lifecycle.** `pending_approval` → `approved`/`waiting_input` → `running` →
  `succeeded`/`failed`; also `denied`, `expired`, `refused`. Actions that need no input start
  on approval; interactive ones (`tether catalog approve`: digest prefix, hidden
  passphrase) wait for the owner to attach a terminal. A restart fails runs that
  were mid-flight.
- **Transcript.** Output only, ANSI-stripped, secret-shaped strings redacted, 64 KiB
  tail. Keystrokes are never stored or logged; the terminal's echo of typed bytes
  is also dropped from the stored copy, and echo-off prompts produce no output.
- **Result.** The run (state, exit code, bounded output, timestamps, user, machine,
  argv, digest) is returned as `action_run` on the approval inbox item and kept in
  `action_runs` (one audit row per run; start and finish are also logged).
- **Hub ssh.** Set `TACHYON_ACTION_HUB_SSH_ADDR` (`host:22`),
  `TACHYON_ACTION_HUB_SSH_KEY_FILE`, `TACHYON_ACTION_HUB_SSH_KNOWN_HOSTS` and
  optionally `TACHYON_ACTION_HUB_SSH_USER` (default `chrispian`). Host keys are
  verified against that known_hosts file. The agent-os key is not gated on the hub
  today, so the allowlist is enforced here; use a dedicated key with a forced command
  before this runs beyond dev.

#### Trusted high-tier assurance integration (not activated)

CW-20261019-0036 implements the shared operation/WebAuthn contract (Tesseract
`01M598CCGC6DNT2PDBTS551T7A`) and Basalisk status packet
`01M5A3B1BWRHZ8R9J6HMYC2T9G`. Ordinary browser high-tier decisions remain refused;
Cerberus retains its existing passkey handoff. These service routes require a
separate workload identity and never accept browser cookies or producer authority.

All three routes use POST JSON under `/api/approvals/trusted/v1`, are uncached,
and return `Cache-Control: no-store`:

| Route | Exact scope | Body |
|---|---|---|
| `/item` | `tachyon-assurance.items_read` | `{item_id, query_id}` |
| `/complete` | `tachyon-assurance.completions_deliver` | `{item_id, completion_id, expected_revision}` |
| `/outcome` | `tachyon-assurance.outcomes_read` | `{item_id, query_id}` |

The workload JWT audience is `tachyon-assurance`. Tokens require a pinned direct
service/device subject and kind, `par == sub`, no session, `kid/jti/iat`, exact
expiry and a maximum 15-minute lifetime. Each request verifies fresh issuer JWKS
and current principal/parent scopes plus issuer/token revocation. Exact scope
membership and server-owned caller-to-producer application allowlists apply;
there is no wildcard or operator fallback. A caller may have only the route
scopes it needs. Item existence outside its allowlist is concealed with 404.

`/item` echoes the fresh caller `query_id` and returns `item` with `item_id`,
`revision`, `status`, `expires_at`, original `operation` and `op_digest`.
Respondable items use status `pending`; expired items are not pending. The
operation must match the closed credential-registration profile and JCS digest.
Basalisk uses this authenticated snapshot for its ceremony and presentation.
Request operation revision and HITL interaction revision are different fields.

`/complete` accepts references only: raw evidence, participant claims and
browser operation snapshots are rejected. It loads the current item and obtains
a fresh authenticated Basalisk completion. Success is `{outcome: <HITL outcome>}`.
Migration 013 commits an authority/proof and authority/completion delivery receipt
with the immutable HITL outcome in one SQLite writer transaction. Expiry and
expected revision are checked at that boundary. Exact current/unexpired retries
return the original outcome across concurrency and restart; changed deliveries
and proof reuse conflict. The receipt is not the provider execution ledger.

`/outcome` echoes `query_id` and returns `item_id`, current `revision`, HITL
`state`, and `terminal_outcome` (null until terminal). This requires current
workload authentication but deliberately does not require fresh human proof or
contact Basalisk. It recovers the authoritative decision after response loss,
proof expiry or Basalisk outage without creating another ceremony or grant.

Missing integration returns JSON 503, invalid authentication 401, insufficient
scope 403, invalid commands 400, and stale/conflicting decisions 409. Expired or
invalid proof refuses; current dependency failures return 503. After an ambiguous
completion delivery, use `/outcome`; do not infer success from a timeout or
blindly create a new operation.

Set `TACHYON_APPROVAL_ASSURANCE_CONFIG` to a host-owned JSON configuration only
when commissioning is complete. It contains no secret values:

```json
{
  "authentication": {
    "issuer": "<verified issuer>",
    "jwks_url": "https://<issuer>/.well-known/jwks.json",
    "callers": [{
      "subject": "<enrolled Basalisk caller>", "kind": "device",
      "applications": ["tether"],
      "scopes": ["tachyon-assurance.items_read", "tachyon-assurance.completions_deliver", "tachyon-assurance.outcomes_read"]
    }]
  },
  "basalisk": {
    "authority": "<persisted Basalisk installation ID>",
    "origin": "https://basalisk.nanite.cloud", "rp_id": "basalisk.nanite.cloud"
  },
  "status_exchange": {
    "origin": "https://<issuer>", "issuer": "<verified issuer>",
    "parent_token_file": "<private enrolled status parent file>",
    "subject": "<enrolled Tachyon status caller>", "kind": "device"
  },
  "current_trust": {
    "origin": "https://<issuer>", "issuer": "<verified issuer>",
    "reader_token_file": "<private narrow trust-reader device file>"
  },
  "audiences": {"tether": "tether"}
}
```

The parent and trust-reader files use shared `security/secrets/tokenfile` custody
(mode 0600); rotations are picked up on subsequent reads. The status parent
exchanges at Tether `/auth/token` for audience `basalisk-status`, sole scope
`basalisk-status.assurance_read`, requested lifetime 300 seconds, no session.
Each status lookup exchanges anew; no status JWT or human assertion becomes an
application credential. HTTPS destinations are pinned and redirects refused.

The client POSTs `{completion_id, query_id}` to Basalisk `/assurance/v1/status`
with a fresh unpredictable query ID and requires an exact echo, bounded no-store
JSON, pinned installation/origin/RP, matching item/revision and selected human/key.
Published `libs/security/webauthn v0.1.0` verifies the original assertion bytes and
operation-bound challenge. JCS canonicalization, closed profile validation,
freshness, current status and self-approval checks are required. Revalidating a
retained completion does not advance its authenticator counter again. Basalisk
owns ceremony spend and durable evidence; Tether independently verifies current
requester authority and consumes its execution continuation once.

The Tether `/auth/workload-trust` client follows contract
`01M5A6FC2ASZEM1YQHQXBVSNNF`: fresh 32-byte query nonce, exact verified-principal
metadata echo, active flag, five-second deadline, no target bearer, no cache.
**That contract initially supports only basalisk-status targets. Tether must
extend credential-bound mint tracking and current checks to the three exact
tachyon-assurance scopes above, and commission Tachyon's narrow reader device,
before this integration can activate.** Unsupported/currently inactive targets
fail closed. No configured production credential or active endpoint is asserted.

Tests exercise signed Ed25519 JWTs, synthetic WebAuthn registration/assertions,
TLS status/exchange/current-trust wire adapters, mismatches/outages/revocation,
recovery, and durable receipt races/rollback. Peer HTTP servers model the wire
contracts; this is not deployed cross-service or real-authenticator acceptance.
Unsigned high-tier denial, browser ceremony UI and owner-attended first-key
registration remain separate work. Existing producer JWT revocation wiring is
also separate from this stricter trusted-caller boundary.

### Themes and the Flight board

`themes` is the public name of the Planner `priorities` database (the old name still
works as an alias; the storage key, the `strategic_priority` kind and `PR-nn` ids are
unchanged). A theme is a feature, capability, topic or whole app that groups
workstreams and tasks. The operator approves and sequences themes; approval means
eligible for sequencing, never launch authorization.

- Statuses: `proposed`, `ready`, `approved`, `paused`, `done`, `archived`. The earlier
  `draft`, `active` and `achieved` stay readable (as proposed, proposed, done) but cannot
  be written, and nothing is ever inferred as approved. `approved_by`, `approved_at`
  and `order` are server-managed.
- Actions on `planner.portfolio.action` (HTTP `POST /themes/{id}/{action}`): `ready`,
  `park`, `unpark`, `append_state`, `verify` are open to agents holding the write grant;
  an agent must give a `reason` for `ready`, `park` and `unpark`, and may park an
  approved theme. `approve`, `resequence`, `finish` and `archive` (decline) are
  operator-only, as is any other route into or out of approval or `order`.
- `GET /board?scope=themes` returns `in_flight`, `cleared`, `queued`, `parked`, `landed`
  (last 14 days by default, `recent_hours`) and, with `include_proposed=true`, `proposed`.
  These words are display only. Progress is computed at read time from the linked
  workstreams' tasks plus the theme's `tasks`: done is Torque `done`; cancelled,
  abandoned and archived tasks leave the total. The Flight tab (`/planner/flight`)
  renders it with operator buttons.

### Reference links for agents

Pattern matching finds ids that look like ids (`CW-…`, a 26-character item id, `namespace#key`). A bare
key such as `approval_execution_2026_10_22` has no namespace and cannot be matched safely, so agents name
what they mean:

- `tesseract:<item_id>` (preferred; every Tesseract write returns the `item_id`), or `tesseract:<namespace>#<key>`.
- `[label](tach:<service>/<id-or-key>)` when a label helps. It is an ordinary markdown link, so it reads as
  its label anywhere the console is not rendering it. Services: `tesseract` (aliases `tess`, `memory`) and
  `torque` (alias `task`); the id may be URL-encoded. An unknown service or id renders as plain text.
- Torque ids stay bare.

A resolved reference reads `Type: Title` (`Handoff: Approval execution`, `Task: …`, `Decision: …`) and keeps the
same hovercard and drawer; until it resolves the chip shows the author's label, else a short form of the id.
Labels are cut at 48 characters (the hovercard carries the full title). Per-device override:
`localStorage['console.refs.labelMax.v1']` (16 to 200), or `setRefLabelMax(n)` in `autolink/labelPrefs.ts`.

### Planner access for agents

`/api/agent/v1/data` is an always-authenticated, uncached service API using the
same local data verbs as the browser. `/api/agent/v1/mcp` exposes those granted
verbs over stateless Streamable HTTP MCP; `tachyon data mcp` remains the stdio
adapter. Every HTTP/MCP request checks credential expiry, revocation and grants.
Cookies and claimed actor headers cannot establish agent authority.

Credentials grant an explicit set of verbs and object kinds. Available presets:

| Preset | Access |
| --- | --- |
| `planner-read` (default) | Original five reads over twelve Planner kinds; existing credentials are unchanged |
| `portfolio-read` | Original reads plus `planner.portfolio.list/get/schema/resolve`, restricted to the seven portfolio kinds |
| `portfolio-write` | Portfolio reads plus `planner.portfolio.create/patch/action/relate/reorder` |

The original reads are `planner.list`, `objects.list`, `objects.get`,
`objects.history`, and `objects.lookup_source`. `objects.list` requires a kind;
unqualified `planner.list` requires all twelve kinds. **For the UI's portfolio,
use `planner.portfolio.list` with `db`.** Generic workstream reads also include
source-owned Tether inventory; the portfolio projection excludes that inventory.

The seven databases are `priorities`, `workstreams`, `roadmap`, `ideas`,
`decisions`, `risks`, and `inbox`. Their grant kinds are respectively
`strategic_priority`, `workstream`, `roadmap_item`, `idea`, `decision`, `risk`,
and `inbox_item`. `--verbs` and `--kinds` narrow or override a preset. A verb grant
and kind grant are both required; relations require both referenced kinds.
Discovery only lists granted verbs. Resolve searches only granted databases.

Write payloads are the existing `planner.portfolio.*` schemas returned by
discovery. Create takes `{db,id,fields}` with a caller-retained UUID. Patch takes
`{db,id,expected_revision,patch}`. Action takes
`{db,id,expected_revision,action,arguments?}`; supported actions are `comments`,
`link-add`, `link-remove`, `decide`, `defer`, `reopen`, and `dismiss`. Reorder takes
`{db,items:[{id,expected_revision}]}`; relate takes
`{from:{db,id},to:{db,id},expected_revision,add,field?}`.
Writes use POST with JSON; reads use GET with the `input` query parameter.
All writes are attributed to `service:<identity UUID>` in history and use the
existing local change stream. Agent input cannot replace the authenticated actor.
Existing IDs such as `PR-10` resolve through portfolio get/resolve; writes use
the returned local UUID and revision. A stale write conflicts; re-read and
reconcile instead of automatically retrying. No agent delete, ownership transfer,
generic object mutation, promotion, execution or upstream-proxy verbs are exposed.
Credentials do not authorize browser operator routes.

Operator-local credential administration uses the app database and serial write
queue. It stores identity records, token hashes, expiry, grants and revocation;
plaintext credentials exist only in private token files. Revocation and identity
disabling are checked on every request. Issue and rotation never print a secret
or overwrite an existing file:

```sh
./tachyon access issue --name tachyon-planner \
  --token-file "$HOME/.config/tachyon/tokens/planner-read.token" --ttl 720h
# Separate principal for an authorized writer; does not expand the read token.
./tachyon access issue --name portfolio-architect --preset portfolio-write \
  --token-file "$HOME/.config/tachyon/tokens/portfolio-architect.token" --ttl 720h
./tachyon access list
./tachyon access revoke --id TOKEN_UUID
./tachyon access disable --id IDENTITY_UUID
```

For rotation, issue with the same `--identity IDENTITY_UUID --name NAME` to a new
file, switch the client to that file, then revoke the previous token. Lifetime
must be between one minute and 90 days. Optional `--verbs` and `--kinds` accept
comma-separated sets of supported verbs and twelve Planner kinds.
`TACHYON_DATA_DIR` selects the operator's database; agents must not use these local
administration commands. The current host and worker share a trusted OS user;
file permissions are not an isolation boundary against other processes with
that same UID.

Both the CLI and stdio MCP adapter use the remote API and re-read the private
0600 token file on every call. HTTPS is required except for loopback IPs, and
redirects are refused. Place flags before the verb:

```sh
export TACHYON_API_URL=https://tachyon.nanite.cloud
export TACHYON_API_TOKEN_FILE="$HOME/.config/tachyon/tokens/planner-read.token"
./tachyon data verbs
./tachyon data --input '{"kind":"workstream","limit":50}' planner.list
./tachyon data --input '{"id":"LOCAL_UUID"}' objects.get
./tachyon data mcp
```

With a portfolio credential, use `planner.portfolio.schema` to learn current
native statuses, and `planner.portfolio.get` to obtain the local revision:

```sh
./tachyon data --input '{"db":"risks"}' planner.portfolio.list
./tachyon data --input '{"id":"PR-10"}' planner.portfolio.resolve
./tachyon data --input '{"db":"ideas","id":"LOCAL_UUID","expected_revision":2,"action":"comments","arguments":{"text":"Reviewed the proposal."}}' planner.portfolio.action
```

MCP tools use names such as `tachyon_planner_list` and `tachyon_objects_get`,
with the same input schemas and result envelope as HTTP. Configure the client
with the absolute Tachyon binary path, arguments `["data", "mcp"]`, and the two
environment variables above. Only the token **path**, never its contents, goes
into client configuration. Restart MCP after changing the granted verb set to
refresh discovery; changed permissions are enforced immediately by the API.
Remote MCP clients use `https://tachyon.nanite.cloud/api/agent/v1/mcp` with their
own bearer credential. Tether catalog entries use `token: file:///PRIVATE/PATH`,
not a literal token. Separate writer identities preserve agent attribution.
Never route agent writes through a shared read identity or `local-data`.

The portfolio integration bundle is `config/tether/portfolio-access-catalog.json`.
It adds read-only origin `tachyon`, separate writer origins `tachyon_pa`
and `tachyon_pm`, and the corresponding Portfolio Architect/Manager launch
grants and boot instructions. Writer origins prepend `tachyon_pa_` / `tachyon_pm_` to tool names
to avoid collisions: for example `tachyon_pa_tachyon_planner_portfolio_patch`.
The shared read origin retains `tachyon_planner_portfolio_list`. File references
must exist privately on the hub (and any worker directly connecting upstreams).
The bundle requires catalog plan/approval/apply/reload; a file in this repository
is not evidence of activation. Existing sessions need a fresh MCP connection and
updated boot context; the bundle does not restart them.

The reviewable catalog bundle is `config/tether/planner-catalog.json`. A Tether
connection with `catalog.write` submits it to `tether_catalog_plan`; the operator
then approves its returned digest with `tether catalog approve PLAN_ID` on the
hub, and that connection applies and reloads the catalog. The draft bundle
passes Tether's scratch catalog validation. The MCP adapter must include
`catalog.write` in its configured scopes to submit the plan; approval remains
a separate operator action.

Once the dedicated `tachyon-planner` Tether catalog agent and launch are approved
and installed, start or recover the retained planner launch from this checkout:

```sh
./scripts/boot-planner
# After that session has ended, explicitly replace its retained launch key:
./scripts/boot-planner --replace
# Start an additional independent planner:
./scripts/boot-planner --key planner-second
```

The script launches through the operator's existing SSH access to the Tether hub
CLI, using the approved catalog profile and an explicit `agent-os` worker. The
catalog holds the agent's prompt, permissions, data-client path and private
Planner credential path; the script carries no secrets. `--hub`,
`--tether-binary`, `--catalog`, `--worker`, and `--launch` select those deployment
coordinates. `--print-command` shows the exact quoted command without launching.
Retry an uncertain launch with the same key to recover its existing session.

The paired-device HTTPS launch path currently drops the profile's `tether.read`
grant because Tether compares it literally against the device's `read` scope.
The attempted `/auth/token` exchange also returned 401 (verified caller identity
required). The SSH CLI path is verified; no identity enforcement or worker
fallback was disabled. Those HTTP integration issues belong to Tether.

Use the session ID printed by the script to interact through the hub CLI:

```sh
ssh -t chrispian@100.74.49.44 \
  '/var/lib/fabric/bin/tether --catalog /var/lib/fabric/tether/catalog sessions turn SESSION_UUID "Summarize the active workstreams"'
ssh -T chrispian@100.74.49.44 \
  '/var/lib/fabric/bin/tether --catalog /var/lib/fabric/tether/catalog sessions peek SESSION_UUID'
```

The planner reads through the CLI, reports what is present, then waits for the
next turn without polling. It has a narrow Claude Bash allowlist for the data
client and no blanket permission bypass. Its initial read can be empty for
locally owned kinds not yet populated. This dedicated agent remains read-only.
The separate portfolio writer grants do not change its charter or credential.
No background agent restart job is enabled. Torque Planner retirement is a
separate coordinated task; Torque execution-task APIs remain in use.
