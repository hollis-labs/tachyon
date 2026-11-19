# Builder catalog index (B1)

The Builder's read model of the Tether catalog (design `design_tachyon_builder_registry_backend_20261030`
section 3.1-3.2). Tether stays the system of record; the index is rebuildable. Package `internal/catalogindex`,
migration `030_catalog_index.sql`.

## What it does

- A poller (30 s) lists the catalog files, reads those whose listing fingerprint moved (or, when the listing has
  none, whenever the newest plan id moved or a 5-minute full scan is due), hashes the content (`sha256`, the
  revision of an entity), parses it into typed rows and keeps a masked snapshot of every change.
- Only `agents/`, `launches/`, `projects/` and `skills/` are read. `mcp-servers/`, `providers/`, `sandbox-profiles/`
  and `global.yaml` are never read (credentials); `mcp-servers/` is counted by presence for the tool-server badge.
- Snapshots are written when the Builder applies a plan (`source: builder`, with plan id and actor, via
  `Index.NoteApplied`, called by the change service in B3) and when a sha change is observed (`source: observed`).
  Retention: newest 50 per path and 90 days.
- Events: `catalog {observed_at, changed_paths[], source}` (replayable) and `summary` (live snapshot) on
  `GET /api/plugins/console/v1/builder/stream`; `GET .../builder/summary` is the tab-count call. The first pass
  after a fresh install publishes no catalog event.

## Secrets

Environment values (`env`, `overrides.env`, `provider_overrides`), values under secret-named keys
(key/secret/token/password/credential/auth) and secret-shaped strings are replaced by `***` before anything is
stored; key names and a secret-like count are kept. YAML is edited as a node tree, so comments, key order and
unknown keys survive. Content that does not parse is skipped, never stored raw. Nothing is logged but paths.

## Settings

`TACHYON_CATALOG_INDEX=0` turns it off. It needs `TETHER_ADDR` and the read-scope `TETHER_TOKEN_FILE` (shared with the
Agents collector); it refuses a credential over plain http to a non-loopback host. With `TACHYON_BUILDER_STUBS=1`
the stub keeps the whole prefix and the real summary/stream are not mounted.

## Known gaps

- The Tether wire shapes (`GET /catalog/files`, `?path=`, `GET /catalog/history?limit=1`) are UNVERIFIED against a
  live Tether; parsing is tolerant (bare array or wrapped under files/items/entries; content or body).
- `launches.live` is null until B2 (`used_by`); `open_changes`, prompts and policies are 0 until B3/B5.
- Index rows are not yet served as agents/launches lists; that is B2.

## Read model: agents and launches (B2a)

`internal/builderread` serves `GET /agents`, `/agents/{id}`, `/agents/{id}/used_by|checks|history`, `/launches` and
`/launches/{id}` from the catalog index (own read-only SQL over `catalog_entities` and `catalog_snapshots`). They
mount one pattern each, behind `operator.RequireOperator`, only when the index is on and `TACHYON_BUILDER_STUBS` is
off. Shapes follow `internal/builderapi/openapi.yaml`.

- Everything is derived from masked snapshots; environment values never reach a response. Launch `env` is key names
  plus `secret_like_count`. The tests plant secrets and fail if one appears in any body.
- Paging uses `limit` (default 25, max 100) and a `c<offset>` cursor.
- **Live data** (`live_sessions`, `live_count`) comes from Tether `GET /sessions?state=running`, cached 5 s. When Tether
  is unreachable or not configured these fields are `null`, never `0`. The session wire names (`launch_id`,
  `logical_agent_id`) are UNVERIFIED; a session without a launch still counts for its agent.
- `used_by`: `teams` and `assigned_items` are `null` (no source yet). `extended_by` comes from the index.
- `checks`: `references_exist` (skills, tool servers, extends), `model_set`, `extends_cycle`. `tether_validate`,
  `part_drift` and `scope_review_age` need Tether and the Library, and are not produced yet.
- `history`: one entry per kept snapshot, newest first; `diff` is a unified diff against the previous snapshot (null
  for the oldest, or when a file exceeds 3000 lines). `privileged`, `activation` and `backup_id` are empty until the
  change flow (B4) records them; `plan_id` and `actor` are set for snapshots written by the Builder.
- Agent detail reads the editor sections from the masked YAML on a best-effort basis; `parts` is empty until the
  Library (B2b).

## Read model: library and resolved (B2b)

Added to `internal/builderread`, same mount and guard as B2a: `GET /library/{kind}`, `/library/{kind}/{id}`,
`/library/{kind}/{id}/used_by|history` and `GET /agents/{id}/resolved[?launch=]`.

- **Backing:** `skills` come from the catalog skill files (masked content, `snapshot_seq`, 2000-byte `body_preview`).
  `tool_servers` come from the listed `mcp-servers/` paths, which are never read (credentials): rows are presence only
  (`status: "unknown"`, no health, transport, tool count or scopes; `read_only`). `prompts` and `policies` are
  Tachyon-owned parts (B3, held): lists are empty and items read 404 until B3 lands.
- **used_by:** agents (and their launches) that reference the item; projects too for tool servers.
- **history:** skill snapshots, newest first, with `snapshot_seq`, `sha`, `actor`, `plan_id`. Tool servers have none.
- **resolved:** always `resolved_by: "tachyon_approx"`. It walks `extends` (cycle-safe, first definition wins for
  `permission_mode`, `network`, `default_sandbox`), unions skills, tool servers (plus the launch's project servers with
  `?launch=`) and `mcp_scopes`, and sizes prompt parts at bytes/4. Tether's own resolution route is unverified and not
  used; `notes` says what could not be known (health, missing parents or skills). The policy and prompt keys it reads
  from the agent YAML (`permissions.network`, `permissions.default_sandbox`, `permissions.mcp_scopes`,
  `prompt.system_prompt|agent_prompt|body`) are best effort and UNVERIFIED against real catalog files.

## Tool server health and reference integrity (B4)

- `builderread.ToolSource` supplies tool server health (`transport`, `tags`, `status`, `health`, `tool_count`,
  `restarts`) to the Library tool server rows. **No HTTP implementation is wired**: the Tether route behind the design's
  "gateway status" is unverified (the gateway status an agent sees describes MCP origins, not catalog `mcp-servers`).
  With no source, or when the source fails, a tool server reads `status: "unknown"` with null counts, never a guessed
  `ok`. `scopes` stay null: the file is never read (credentials).
- `builderread.CheckUnreferenced(ctx, reader, kind, id)` is the reference-integrity check the change service (B3) calls
  before deleting a skill or tool server. It returns `*Referenced` (agents, launches, projects; code `referenced`, to be
  written as a 409) while anything points at the item, nil when free, and a plain error when the index cannot be read.
  Callers must refuse on any non-nil error: unknown is not free. Launches are matched through their agent; boot
  profiles and teams-policy are not indexed, so Tether's own validation stays the backstop.
- Skill create, edit and delete are B3's; this slice adds none.

## Read model: registry projects (B6)

`GET /registry/projects[?q=&limit=&cursor=]` and `GET /registry/projects/{id}` (read only; the PATCH belongs to the
existing allowlisted proxy and B3). Mounted by the same `builderread.Reader.Mount` and guard as B2.

- **Registry source:** Tether `GET /registry/projects?status=*&include=external_ids` with the read token (the wire
  `internal/objectsync` already scans), cached 10 s. If the registry is not configured or unreachable the routes answer
  503 `registry_unavailable`, never an empty list. Projects with `merged_into` or status `merged` are folded away.
- **Ids (UNVERIFIED):** the registry has no documented short id, so the Builder id is `props.id`, then `props.slug`,
  then the last URN segment, then the slugged display name (first that is a valid id; duplicates are dropped).
- **Link to the catalog (UNVERIFIED):** `props.catalog_id`, then a catalog-substrate external id; if neither is set the
  registry id, then the slugged display name. Exactly one catalog project must match to read `linked`; none or several
  read `unlinked` with a `reason`, and then `catalog` and `consumed_by.agents|launches` are null.
- **Composed fields:** identity and guidance from the registry; `catalog` (`repo_root`, `workspace.mode`, `mcp_servers`
  null = inherit, `sha`) from the masked project file; `consumed_by.agents|launches` from the launches in that project;
  `tether_sessions.live` from running sessions on those launches (`total` is null: Tether has no per-project total here).
- **Not available:** `planner_items` is null (no planner count is wired); `machines`, `services`, `repos` stay `soon`.
