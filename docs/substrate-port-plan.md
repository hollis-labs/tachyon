# Substrate port plan: Tachyon

Status: **plan, for review and approval. No port code exists yet.** Torque: CW-20261112-0096 (wave 3, in use, cutover).
Follows Substrate `docs/adopting-substrate.md` (section 11 is the cutover) and the shape of a finished in-use port
(Station `docs/cutover-plan.md`). Owner rule: **code is a clean break, data is not.** Every row Tachyon holds today is
migrated; loss is not acceptable.

Nothing in this plan touches the running service, its data directory, its unit or its descriptor. Every data step here
runs on a copy. The cutover itself is run by the program director with the owner, not by the port cell.

## 0. Answers first (adopting-substrate section 0)

| Question | Answer |
|---|---|
| Migrated data or fresh start? | **Migrated.** In use daily; two SQLite files (`tachyon.db`, about 130 MB, 56 schema migrations; `jobs.db`, about 25 MB) and on-disk state beside them (section 5). |
| Binary variant | `app-host` (embedded SPA, same-origin API, SSE streams, subprocess plugins). |
| Who issues identity? | Tachyon itself, through the `identity` module: one owner created offline, API keys for every machine caller (section 4). |
| Routes: public, scoped, self-authenticating | Every `/api/` route gets a `tachyon.<verb>` scope or `authenticated`. Public: `/healthz` and the identity module's own sign-in routes. No open mount is planned; if the trusted approval completion path (`/api/approvals/trusted/v1`) must keep its own workload-JWT check, it becomes the one `Mount{Open: true}` and says so in code. |
| Background work that must drain | Object sync (scheduler), chat sync loop, agents collector, catalog index, card delivery/event outbox, alert sources, transcript snapshotting, action runs. Scheduler-shaped work moves to the `scheduler` module; intrinsic loops stay app goroutines started from `serve` and joined before modules stop. |
| What the UI needs from the framework | app-shell with BOM-pinned kits; Tachyon's nav and surfaces in its own contribution file (`frontend.contributions`); sign-in/sign-out from `kit-identity`. |
| Libs not in the BOM, or pinned differently | Yes; section 10 lists them. Each is a Substrate BOM PR filed as a `substrate-gap` task, not a local `go.mod` exception. |

## 1. Where the port lives

Ruling from the director (2026-11-14): port code never merges to `main` while the old app is live; the live unit and routine
deploys build from `main`.

- **Branch.** All port PRs target the long-lived branch **`substrate-port`** (created from `main` at `d123471`, 2026-11-14).
  Only the cell lead merges into it, under the same rule as `main`: reviewer approve on the head SHA, CI green,
  `gh pr merge --squash --match-head-commit <sha>`. This plan (docs only) goes to `main`.
- **A parallel tree, not an in-place rewrite.** The new app is built in **`next/`**, a separate Go module
  (`github.com/hollis-labs/tachyon/next`, own `go.mod`) with its own frontend workspace. The old tree (`cmd/`, `internal/`,
  `plugins/`, `frontend/`, `packages/`, `pkg/`) is not edited on `substrate-port`. Reasons:
  1. `main` keeps moving (several PRs a day). An untouched old tree makes every `main` → `substrate-port` merge conflict-free,
     so the live app's fixes reach the branch without hand-resolution.
  2. The BOM pins some libs at versions different from the old tree's (section 10). A separate module lets `next/` sit exactly
     on the BOM while the old binary keeps its pins.
  3. The old tree stays the reference beside the new one (the guide's `<app>-previous`), and its tests stay runnable.
  Root `go vet ./...` does not descend into a nested module, so root CI is unchanged; a second CI job builds and tests `next/`.
  The root `package.json` workspaces do not include `next/`.
- **Carrying `main` across.** The lead merges `main` into `substrate-port` (a merge commit, not a rebase: the branch is shared)
  at least every working day and before every rehearsal. Each sync records, in a Torque comment on the port task, the old-tree
  paths that changed since the previous sync (`git diff --stat <prev-sync>..<new-sync> -- cmd internal plugins frontend packages pkg`).
  Each behaviour change in that list becomes either a **parity-carry** task (port the change into `next/`) or a recorded "no
  effect on the port". A new migration file in `internal/localstore/migrations/` always becomes a parity-carry task, because
  the data migration must know the new schema.
- **Promotion.** When parity is reached and the first full rehearsal passes, one PR on `substrate-port` deletes the old tree
  and moves `next/` to the repository root (`cmd/tachyon`, `internal/...`, `web/`). From then on, `main` merges into the branch
  conflict on deleted files; each is resolved by porting the change (or recording no effect) and taking the deletion. The final
  rehearsal runs on the promoted tree, so the build that is cut over is the build that was rehearsed.
- **Reaching `main`.** At the cutover only, as the director's step (section 8).

## 2. Target layout

```
next/
  go.mod                        module github.com/hollis-labs/tachyon/next (BOM pins only)
  cmd/tachyon/
    main.go                     boot.StartWith + cobra root (serve default, migrate-from, plus the kept subcommands)
    substrate.yaml              manifest
    substrate_wiring.go         generated, committed
    ui/app.json                 Tachyon's nav items and surfaces (frontend.contributions)
  internal/
    settings/                   decodes config.tachyon, refuses a bad key naming it
    migratefrom/                the one-way data migration (section 5)
    <domain packages>           ported from internal/* (section 3)
    webui/                      embeds all:dist (dist/.gitkeep kept)
  plugins/console, plugins/planner   subprocess plugins, ported, served through pluginhost
  web/                          the SPA on app-shell (GUI track, section 9)
  docs/architecture.md, docs/conversion-notes.md
```

Manifest (target; slice 1 settles the exact keys):

```yaml
app: tachyon
variant: app-host
profiles: [minimal]
modules:
  add: [httphost, database, auth, identity, secrets, cli, scheduler, pluginhost]
config:
  httphost: { addr: "127.0.0.1:8093" }
  database: { sqlite: { path: tachyon.db } }
  auth: { mode: enforce }
  identity: {}
  tachyon: { }   # app block: upstream URLs, sync intervals, feature switches (section 6)
```

`mcp` is added only if the inventory finds a live MCP caller of `/api/agent/v1/mcp` (section 7); otherwise that route is ported
as plain app code behind its scope, as today.

## 3. Module mapping

| Today | On Substrate |
|---|---|
| `cmd/tachyon` env parsing, `host.New` (libs app-host), `net.Listen` | `boot.StartWith`, `httphost.Handler`/`Serve`, manifest config + `SUBSTRATE_CONFIG` override |
| `internal/localstore` (own `sql.Open`, `serialwrite` queue, `tachyon_migrations` ledger, 56 SQL files plus the base DDL in `store.go`) | `database` module store (`database.From(app)`): one writer, `Reader()` pool, `WriteTx`. **One goose migration** (`00001_init.sql`) that is the schema as of the last old migration, collapsed: no `ALTER` ladder, no rebuild leftovers (`*_next`, `*_new`, `bus_events_055`) unless the inventory shows they hold rows. |
| `jobs.db` (libs `core/util/scheduler/sqlstore`, a second file to avoid a second writer) | `scheduler` module, which uses the same `sqlstore` schema inside the main database. One file. |
| `pkg/pluginkit`, `PluginBinDir`, app-host plugin wiring | `pluginhost` module (`pluginhost.UseSpecs`), forwarding `/api/plugins/{id}/...` under the auth guard; per-plugin scopes on `auth.ScopeTable` |
| Tangent bridge browser session, `TACHYON_ASSURANCE=dev`, `/api/session*` elevation | `identity` module (owner session cookie, `X-Requested-With` + same-origin on writes). The `assurance.Provider` interface stays app code with a new identity-backed implementation (section 4). |
| `internal/agentaccess` service identities and hashed service tokens | Credentials: identity API keys. Service identity records, agent bindings and verb grants: app data, migrated (section 4). |
| `libs/security/secrets/tokenfile` for upstream tokens | `secrets` module resolver (`file://` references keep working) |
| `os.Getenv` for every setting | `config.tachyon` keys, `internal/settings` (section 6) |
| `/healthz` (app-host) | `health` module `{status, checks}` plus checks for the database, scheduler and plugins |
| `TACHYON_*_STUBS` switches | Dropped from the production build; tests use fakes |

## 4. Auth, identity and credentials

- **Browser.** The owner signs in with the identity module (offline bootstrap at cutover, section 8). The Tangent session
  bridge and its cookie are removed; Tachyon stops calling Tangent for sessions. That removes a Tachyon→Tangent call only;
  Tangent itself does not change.
- **Assurance.** Live runs `TACHYON_ASSURANCE=dev` (every request is an elevated operator session). The port keeps the
  `assurance.Provider` seam and its table (approvals-assurance.md), implemented on identity: `browser-session` = a signed-in
  person; `operator-token` = a signed-in person holding `tachyon.operator` (the owner holds it through `*`). This is parity with
  the live posture and is stricter than it (an unauthenticated request is refused). Re-authentication step-up before a
  high-risk resolve is not in the identity module today; it is a recorded follow-up (CW-20261030-0082 stays open), not a port
  target. The confirmation phrase for high and cerberus items is app code and is ported unchanged.
- **Machine callers.** Today each agent holds an opaque token whose hash is in `service_tokens`, checked against the verb grants
  of its service identity. The hashes cannot become identity keys (only the hash is stored). Decision: **every holder gets a new
  identity API key at the cutover**, named after its service identity, with scopes `tachyon.<verb>` equal to its current grants
  (one scope per verb, so no grant widens). `service_identities`, `service_identity_agents`, `agent_launch_bindings` and the
  grant presets are migrated as app data; `service_tokens` rows are migrated as revoked history (no live credential). The key
  files are written mode 0600 by `tachyon identity key create --out` and handed to each holder's owner in the window, as Station
  did. The per-verb check in `dataverbs` reads the verified principal's scopes instead of the token row.
- **Approval producers** that complete approvals over `/api/approvals/trusted/v1` with workload JWTs keep that protocol; the
  inventory confirms each producer and whether its issuer is unchanged.
- **Route scopes.** A test asserts every registered route has a scope (and one deliberately broken route fails it).
- **Strict-mode proofs** (`libs/security/webauthn`, used by the Basalisk assurance profile) are not ported, per the guide;
  recorded as a follow-up with the owner of that flow.

## 5. Data migration

**Shape.** `tachyon migrate-from --source <old data dir>` creates the new database in an empty data directory, applies the
goose schema, opens the old `tachyon.db` and `jobs.db` **read-only** (`mode=ro`, `immutable=1` on a copy), and copies every table
in one write transaction per table group. Domain rows are copied with their ids, timestamps and blobs byte-for-byte; no row is
re-derived from an upstream during migration (caches are copied too, so the first start is warm). It refuses a target that
already has rows, and a source whose `tachyon_migrations` ledger is not exactly the set the migration was built for (a new old
migration is a parity-carry, section 1). It never writes the source.

**What is copied.** Every table in the old schema. The appendix (section 12, from the builder inventory) lists each table with
its owning package, row count on the latest copy, and class: *source of truth* (planner items, objects and revisions, chat
threads/turns/publications, approvals and decision log, alerts, cards, catalog changes, forge audit and drafts, settings, service
identities, transcripts snapshot, terminal sessions, action runs, ...), *derived but copied* (indexes, caches, cursors, watermarks:
copied so sync resumes where it stopped instead of re-pulling or double-delivering), or *dropped* (the old `tachyon_migrations`
ledger, replaced by goose's; rebuild leftovers with zero rows). A table is dropped only if it is listed here with a reason and has
zero rows on the rehearsal copy.

**jobs.db.** The scheduler tables are copied into the main database (same `sqlstore` schema). Schedules keep their ids, cron
expressions, enabled state and next-fire times, so object-sync schedules that are switched on stay on and none fire twice.

**On-disk state beside the databases.** `planner/`, `console/` and `plugins/` under the data directory and the cache directory
are copied into the new data directory with modes kept (0700 dirs, 0600 files). The old `backups/` directory and the
`tachyon-before-*.db` files are **not** copied and **not** touched: they stay where they are as part of the old data directory.
Configuration (`app-agent.json`, the cache policy, token files, the SSH key for the action hub) is referenced by path from the new
config, not copied; secret files are never printed or moved (section 6).

**Verification, built into the command and repeated by hand in the rehearsal.**
1. Before: `PRAGMA integrity_check` = `ok` and `PRAGMA foreign_key_check` empty on the source copy; `sha256sum` of each source file.
2. Per table: row count source = target; an order-independent checksum of every copied column (hash of the sorted per-row
   hashes) source = target. A mismatch fails the run and leaves the target for inspection.
3. After: `integrity_check` and `foreign_key_check` on the target; goose version = the expected head; source checksums unchanged.
4. The command prints a report (table, rows, checksum, class) and writes it beside the new database; the rehearsal attaches it to
   the Torque task.
5. Functional check on the migrated copy: start the new binary against it (scratch home, no upstream tokens), sign in, and read
   one record of each major kind through the API (a planner item, an object with history, a chat thread with turns, an approval
   with its decision log, an alert, a catalog entity, a transcript) and compare with the old binary started on the same copy.

**Rehearsal.** Always on a fresh `.backup` copy of both files, taken while the old unit runs, in the cell's scratch directory with
`HOME`, the `XDG_*` variables and the data/cache directory variables pointed there. At least: one rehearsal when the migration
lands (slice D1), one after promotion, and one on the day of the cutover from that day's copy, timed (it sets the window length).
Every rehearsal records counts, checksums and duration on the task.

**The isolated planner-verification instance** (a second, separate unit with its own data directory, used for planner checks)
is not the live app. It is not migrated; it is listed for the director to retire or keep running on the old build.

## 6. Configuration

Every environment variable the old binary reads maps to a config key in `config.tachyon` or a module block, or is dropped on
purpose; the full table goes in `next/docs/conversion-notes.md`. Groups:

- Listen address (`LISTEN_ADDR`, `TACHYON_ADDR`) → `httphost.addr`. Data/cache directories (`TACHYON_DATA_DIR`,
  `TACHYON_CACHE_DIR`) → resolved in `AfterConfig` (the one allowed env override), absolute paths written into `app.Config`.
- Upstreams (Tether, Torque, Tesseract, Cerberus, Fragments, activity, approval queue, events hub, GitHub org) → `tachyon.upstreams.*`
  URLs plus `secrets` references for each token file. `TORQUE_API_TOKEN` and `GITHUB_TOKEN` (raw values in env today) become
  secret references; the fallback to running `gh auth token` is dropped.
- Feature switches (`TACHYON_AGENTS_COLLECTOR`, `TACHYON_CATALOG_INDEX`, `TACHYON_CARD_EVENTS`, `TACHYON_CARD_EFFECTS_TORQUE`, ...)
  → booleans under `tachyon.features`. Stub switches are dropped. `TACHYON_ASSURANCE` is dropped (section 4).
- Policy/config files (approval producer and assurance config, cache policy, terminal policy, card delivery, app-agent config)
  → path keys.

The live override file is written for the cutover from the live env file's **names** (values are carried by the operator, never
by this cell), and dry-run validated with `tachyon validate` against a scratch copy.

## 7. Clients

Each caller keeps working unchanged or has a named change and an owner. The appendix (section 12) carries the inventory with
evidence; the classes are:

| Caller | How it reaches Tachyon today | Change at cutover | Owner |
|---|---|---|---|
| The owner's browser (the Tachyon UI through the HTTPS proxy) | same-origin, Tangent session for approvals | signs in with the identity module; same URL | owner; proxy unchanged (same port) |
| Agents with service tokens (`tachyon data`, `tachyon access`, the app-chat agent, MCP/data verbs) | bearer token, `/api/agent/v1/*` | new identity API key per service identity, handed over in the window | the director, with each agent's owner |
| Approval producers (trusted completion, HITL) | workload JWT / bearer to `/api/approvals/*`, `/api/hitl` | none expected; confirmed per producer by the inventory | the producer's app |
| Subprocess plugins `console`, `planner` | spawned by Tachyon, local | built and spawned by `pluginhost`; no external change | this cell |
| Cerberus descriptor `tachyon-backend-dev` and the systemd unit it writes | runs `make all` in the working copy | new build/run entry (section 8); handed to the descriptor's owner | the director |
| Tachyon's own CLI subcommands run by people or scripts (`sync-objects`, `import-planner`, `backfill-session-links`, `elevation-ttl`, `access`, `data`, `local-data`) | the binary, data dir by env | kept as cobra subcommands on the same boot; one-shot importers already run (`import-planner`, `backfill-session-links`, `cmd/import-*`) are dropped if the inventory shows no remaining use | this cell |

Upstreams Tachyon calls (Tether, Torque, Tesseract, Cerberus, Fragments Engine, the events hub, GitHub) do not change; only how
Tachyon holds their tokens does.

## 8. Cutover sequence (run by the director with the owner)

Before the window (no downtime):
1. `substrate-port` promoted, parity-carry list empty, CI green, final build artifact chosen (run id recorded).
2. Rehearsal on that day's copy passes (section 5); duration recorded.
3. Prepared, not applied: the override file, the new descriptor entry, the key list (one line per service identity with its
   scopes), the proxy check (same port, no change expected).
4. Every key holder named and ready to swap.

The window:
1. Stop the old unit. Take the final `.backup` of `tachyon.db` and `jobs.db`; `integrity_check` = `ok`; record checksums. Copy the old
   binary and the old env file aside (rollback).
2. `tachyon migrate-from --source <old data dir>` into the **new, empty** data directory; report attached; counts and checksums match.
3. Offline bootstrap: `tachyon identity setup --username <owner>` (password on stdin), then `tachyon identity key create --out ...` per
   service identity.
4. Apply the descriptor change (new binary, override file, the new data directory); start the new unit.
5. Section 10 checks pass, then hand out the keys; each holder confirms one call.
6. Merge `substrate-port` into `main` (the director's step), so the next routine deploy builds the new app.

The old data directory is never written by the new build and is left in place; the owner decides when to delete it.

## 9. Rollback

Stated before the window. Any failed check in section 10, or a holder that cannot work, within the agreed window:
stop the new unit, restore the old descriptor entry and env file, start the old binary on the **old data directory, untouched**.
Rows written by the new build during the window are lost on rollback; the window is kept short and writes are limited to the
checks until accepted. After acceptance there is no automatic rollback; a later rollback needs a reverse export, which this
plan does not build (the director decides at acceptance). `main` is merged only after acceptance, so a rollback never needs a revert there.

## 10. Live verification (adopting-substrate 11.6, plus Tachyon's own)

`/healthz` ok (database, scheduler, plugins); `GET /api/setup` → `setup_required: false`; sign in through the real proxy URL; one
write and one read (a planner item edit, then read back); an unknown `Host` refused; an unauthenticated `/api/` call refused;
each SSE stream (console events, activity) connects and receives a keepalive; object-sync schedules listed with the same enabled
state as before; one approval listed with its history; each key holder's one call succeeds.

## 11. Slices (each a PR into `substrate-port`, each a Torque task)

Backend (`@mig-tachyon-builder`):
- **B1 skeleton**: `next/` module, manifest, wiring, settings, `serve` with health, webui embed, CI job for `next/`, route-scope test.
- **B2 store**: goose init schema (collapsed from 001–056 + base DDL), `database` module wiring, the store discipline (reads on
  `Reader()`, writes in `WriteTx`), ported `localstore` callers' contract tests.
- **D1 migrate-from**: the command, verification report, the rehearsal on a copy (first rehearsal record).
- **B3 identity and access**: identity module, assurance provider on identity, agentaccess on keys + scopes, approvals session
  path without the Tangent bridge.
- **B4..Bn domains**, each with its tests ported: planner (+ planner plugin), objects and object sync (scheduler module), chat
  (threads, topics, workspace, terminal, bridge, cards), approvals, alerts, agents (collector, serve, derive, assign), forge,
  catalog and builder, fileview, transcripts, settings registry, action runs and provider actions, card delivery/events.
  Order follows dependencies (store, identity first; planner and objects before chat and forge).
- **B-cli**: kept subcommands on cobra.
- **P1 promotion** (section 1), then the final rehearsal.

GUI (`@mig-tachyon-gui`), against the API contract agreed in B1 (paths unchanged unless a slice says otherwise; flat
`{code, message, details?}` errors; `X-Requested-With` on writes):
- **G1** app-shell + BOM kits + `ui/app.json` contributions + `kit-identity` sign-in, empty surfaces.
- **G2..Gn** surfaces ported from `frontend/` and `plugins/*/ui` (Agent Ops, Task Ops/planner, builder/catalog, chat, approvals,
  alerts/cards, forge, transcripts, settings), each driven in a real browser against the real `next/` backend on scratch data.

Each slice's PR states the evidence of adopting-substrate section 12 for its scope.

## 12. Appendices (filled from the cell's inventories before review)

- A. Data inventory: every table and file, owning package, rows on the copy, class, migration rule.
- B. Client inventory: every caller with evidence (path used, credential, how it finds Tachyon).
- C. GUI inventory: every surface, the kits it uses, what changes.

## 13. Gaps and decisions

Decided in the cell (reversible, recorded here): parallel `next/` module on `substrate-port`; one database file (jobs.db folded in);
migrate caches and cursors rather than rebuild; re-issue machine credentials; elevation = identity session holding
`tachyon.operator` (parity with live); stub switches and one-shot importers dropped.

Substrate gaps (Torque tasks in project Substrate, tagged `substrate-gap`, `cell:mig-tachyon`; the port works around none of them
silently; BOM and identity questions are coordinated with the Substrate program driver):
1. **CW-20261114-0093.** BOM pins older than the old tree uses: `libs/mesh/core` (BOM v0.6.0, Tachyon v0.9.0) and `libs/ui/ui-go` (BOM v0.1.0, Tachyon
   v0.4.0); `libs/mcp/plugin-mcp` is newer in the BOM (v0.6.0 vs v0.3.0, an upgrade the port takes). The port needs the BOM at
   mesh/core ≥ v0.9.0 and ui-go ≥ v0.4.0, or the port downgrades and loses features; a BOM PR is preferred.
2. Identity step-up (re-authenticate before a high-risk action) does not exist; follow-up, not blocking (section 4).

For the director (crosses the cell): the descriptor change and its owner; the isolated planner-verification instance (retire or keep);
the key handover list at cutover.
