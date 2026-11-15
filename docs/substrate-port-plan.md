# Substrate port plan: Tachyon

Status: **plan, for review and approval. No port code exists yet.** Torque: CW-20261112-0096 (wave 3, in use, cutover).
Follows Substrate `docs/adopting-substrate.md` (section 11 is the cutover) and the shape of a finished in-use port
(Station `docs/cutover-plan.md`). Owner rule: **code is a clean break, data is not.** Every row Tachyon holds today is
migrated; loss is not acceptable.

Nothing in this plan touches the running service, its data directory, its unit or its descriptor. Every data step here
runs on a copy. The cutover itself is run by the program director with the owner, not by the port cell.

The repository is public. Facts about the live machine that the cutover needs (addresses, unit and descriptor, credential
locations, exact override spellings) are kept outside it, in the program's Tesseract workspace item `01M7DXSRBN6J6P5CFT4KMAP0AJ`
("live facts" below).

## 0. Answers first (adopting-substrate section 0)

| Question | Answer |
|---|---|
| Migrated data or fresh start? | **Migrated.** In use daily. Three SQLite files: `tachyon.db` (about 125 MiB, 96 tables at migration 056, 97 at 058, 56 schema migrations plus 11 baseline tables created outside the ledger), `jobs.db` (about 23 MiB, the scheduler: 6 operator-enabled schedules and their fire history) and `plugins/launch-ops/launches.db` (one empty table). Appendix A. |
| Binary variant | `app-host` (embedded SPA, same-origin API, SSE streams, one WebSocket, subprocess plugins). |
| Who issues identity? | Tachyon itself, through the `identity` module: one owner created offline, one API key per machine credential (section 4). Today Tachyon has no accounts of its own: browser sessions come through Tangent and agents hold Tachyon's own hashed service tokens. |
| Routes: public, scoped, self-authenticating | Every `/api/` route gets a `tachyon.<area>.<verb>` scope or `authenticated` (B1's `next/docs/api-contract.md`). Public: `/healthz` and the identity module's own sign-in routes. No open mount. |
| Background work that must drain | Object sync (scheduler), chat sync loop, agents collector, catalog index, card delivery and event outbox, card effects, alert sources and sink, transcript snapshotting, action runs. Scheduler-shaped work moves to the `scheduler` module; intrinsic loops stay app goroutines started from `serve` and joined before modules stop. Every outbound loop has a config switch (section 6), which the cutover uses (section 8). |
| What the UI needs from the framework | app-shell with BOM-pinned kits; Tachyon's nav and surfaces in its own contribution file (`frontend.contributions`); sign-in/sign-out from `kit-identity`. |
| Libs not in the BOM, or pinned differently | Yes; section 13 lists them. Each is a Substrate BOM change filed as a `substrate-gap` task, not a local `go.mod` exception. |

## 1. Where the port lives

Ruling from the director (2026-11-14): port code never merges to `main` while the old app is live; the live unit and routine
deploys build from `main`.

- **Branch.** All port PRs target the long-lived branch **`substrate-port`** (created from `main` at `d123471`, 2026-11-14).
  Merges follow the program's process (2026-11-14): when the cell reviewer approves at the head SHA and CI is green, the cell
  lead sends the PR URL and the reviewed SHA to the Substrate program driver, who merges (squash, matched to the reviewed head
  commit). Nobody else merges into the branch. This plan (docs only) goes to `main` the same way.
- **A parallel tree, not an in-place rewrite.** The new app is built in **`next/`**, a separate Go module
  (`github.com/hollis-labs/tachyon/next`, own `go.mod`) with its own frontend workspace. The old tree (`cmd/`, `internal/`,
  `plugins/`, `frontend/`, `packages/`, `pkg/`) is not edited on `substrate-port`. Reasons:
  1. `main` keeps moving (several PRs a day). An untouched old tree makes every `main` → `substrate-port` merge conflict-free,
     so the live app's fixes reach the branch without hand-resolution.
  2. The BOM pins some libs at versions different from the old tree's (section 13). A separate module lets `next/` sit exactly
     on the BOM while the old binary keeps its pins.
  3. The old tree stays the reference beside the new one (the guide's `<app>-previous`), and its tests stay runnable.
  Root `go vet ./...` does not descend into a nested module, so root CI is unchanged; a second CI job builds and tests `next/`.
  The root `package.json` workspaces do not include `next/`.
- **Carrying `main` across.** The lead merges `main` into `substrate-port` (a merge commit, not a rebase: the branch is shared)
  at least every working day and before every rehearsal. Each sync records, in a Torque comment on the port task, the old-tree
  paths that changed since the previous sync (`git diff --stat <prev-sync>..<new-sync> -- cmd internal plugins frontend packages pkg`).
  Each behaviour change in that list becomes either a **parity-carry** task (port the change into `next/`) or a recorded "no
  effect on the port". A new migration file in `internal/localstore/migrations/` always becomes a parity-carry task, because
  the data migration must know the new schema (and `migrate-from` refuses a source it does not know, section 5).
- **Promotion, as late as possible.** Slice P1 deletes the old tree and moves `next/` to the repository root (`cmd/tachyon`,
  `internal/...`, `web/`). It is the **last** slice: it runs only when parity is reached, the parity-carry list is empty and a
  full rehearsal on the unpromoted tree has passed. From P1 to cutover the cell asks the director for a **`main` freeze**: fixes
  only, each one tagged parity-carry and ported by hand (after P1 a `main` change is a modify/delete conflict on the branch). The
  final rehearsal runs on the promoted tree, so the build that is cut over is the build that was rehearsed.
- **Reaching `main`.** At the cutover only, as the director's step (section 8).

## 2. Target layout

```
next/
  go.mod                        module github.com/hollis-labs/tachyon/next (BOM pins only)
  cmd/tachyon/
    main.go                     boot.StartWith + cobra root (serve default, migrate-from, access, the kept subcommands)
    substrate.yaml              manifest
    substrate_wiring.go         generated, committed
    ui/app.json                 Tachyon's nav items and surfaces (frontend.contributions)
  internal/
    settings/                   decodes config.tachyon, refuses a bad key naming it
    migratefrom/                the one-way data migration (section 5)
    <domain packages>           ported from internal/* (section 3)
    webui/                      embeds all:dist (dist/.gitkeep kept)
  plugins/console               subprocess plugin, ported later with pluginhost (deselected for now, section 13; planner not ported)
  web/                          the SPA on app-shell (GUI track, section 11)
  docs/architecture.md, docs/conversion-notes.md
```

Manifest (target; slice B1 settles the exact keys):

```yaml
app: tachyon
variant: app-host
profiles: [minimal]
modules:
  add: [httphost, database, auth, identity, secrets, cli, scheduler]   # pluginhost returns with the console plugin slice (section 13)
config:
  httphost: { addr: "127.0.0.1:<port>" }   # the live port is set in the private override
  database: { sqlite: { path: tachyon.db } }
  auth: { mode: enforce }
  identity: {}
  tachyon: { }   # app block: upstream URLs, sync intervals, feature switches (section 6)
```

The app binds loopback; the operator's HTTPS reverse proxy fronts it, and the bind address and port are the live ones (live
facts), so the proxy needs no change and `httphost.Serve`'s bind check passes under `enforce`. The proxy's host names go into the
identity/host allowlist in the private override, together with `identity.trusted_proxy`, so the session cookie is marked Secure.
Every rehearsal from D1 on checks the real proxy name, not only loopback (an app whose override lacked its allowed host was
unreachable on its proxy name for two days). The old code's default of all interfaces is not carried.

**MCP.** Agents reach Tachyon through the Tether MCP gateway at `/api/agent/v1/mcp` (streamable HTTP). Adoption first
(section 13): the MCP slice uses Substrate's `mcp` module over HTTP if the gateway can point at its path (the gateway path is a
contract with Tether, decided by the director with that slice); otherwise the old route is ported as app code behind its scope.
Either way **there is no stdio `LocalTrust`** in Tachyon; if a later slice adds stdio MCP, it must pass `LocalScopes` that leave out
`tachyon.operator` and the approval-resolve scopes (guide section 5.9).

## 3. Module mapping

| Today | On Substrate |
|---|---|
| `cmd/tachyon` env parsing, `host.New` (libs app-host), `net.Listen` | `boot.StartWith`, `httphost.Handler`/`Serve`, manifest config + `SUBSTRATE_CONFIG` override |
| `internal/localstore` (own open, `serialwrite` queue, `tachyon_migrations` ledger, 56 SQL files plus the baseline DDL in `store.go`) | `database` module store (`database.From(app)`): one writer, `Reader()` pool, `WriteTx`. **One goose migration** (`00001_init.sql`) equal to the old schema at 056 plus the baseline tables, collapsed: no `ALTER` ladder. Kept exactly: columns, types, NOT NULL, defaults, keys, foreign keys, indexes and the `approval_decision_log` append-only triggers (029). Proven by a schema-diff test (B2). Rebuild leftovers are dropped only under the rule of section 5. |
| `jobs.db` (libs `core/util/scheduler/sqlstore` in a second file) | `scheduler` module, which uses the same `sqlstore` (libs `core/util` v0.6.0 on both sides) inside the main database. One file. |
| `pkg/pluginkit`, `PluginBinDir`, app-host plugin wiring | `pluginhost` module (`pluginhost.UseSpecs`), forwarding `/api/plugins/{id}/...` under the auth guard; per-plugin and per-route scopes on `auth.ScopeTable`. Only an explicit allowlist of console paths is forwarded; there is no catch-all. **Deselected for now** (section 13, CW-20261114-0184): the app serves the console data routes itself. |
| Tangent bridge browser session, the `TACHYON_ASSURANCE` posture switch, `/api/session*` elevation | `identity` module (owner session cookie, `X-Requested-With` + same-origin on writes). The `assurance.Provider` interface stays app code with an identity-backed implementation (section 4). |
| `internal/agentaccess` service identities and hashed service tokens | Credentials: identity API keys. Service identities, agent bindings, proxy pin, per-credential grants (verbs and kinds) and the credential-to-identity mapping: app data (section 4). |
| `libs/security/secrets/tokenfile` for upstream tokens, credentials passed as environment values | `secrets` module resolver (`file://` references) for every upstream credential |
| `os.Getenv` for every setting | `config.tachyon` keys, `internal/settings` (section 6) |
| `/healthz` (app-host) | `health` module `{status, checks}` plus checks for the database, scheduler and plugins |
| `TACHYON_*_STUBS` switches | Dropped from the production build; tests use fakes |

## 4. Auth, identity and credentials

- **Browser.** The owner signs in with the identity module (offline bootstrap at cutover, section 8). The Tangent session
  bridge and its cookie are removed; Tachyon stops calling Tangent for sessions. That removes a Tachyon→Tangent call only;
  Tangent itself does not change.
- **Assurance.** The old build's posture is set by `TACHYON_ASSURANCE`; the live setting is in the live facts. The port keeps the
  `assurance.Provider` seam and its table (`docs/approvals-assurance.md`), implemented on identity:
  - `browser-session` = a verified principal of kind **user** (a signed-in person, or a token minted from that person's session).
  - `operator-token` = a principal of kind user **and** holding `tachyon.operator`. The owner holds it through `*`.
  - An API key, or a token minted from a key, is never `operator-token` and never `browser-session`, **whatever its scopes**
    (`identity key create` can grant `*`). Test: a key holding `*` is refused every browser-session and operator-token action.
  - `tachyon.operator` is never granted to a key (the `access` command refuses it).
  No request is treated as elevated without a signed-in person, and an unauthenticated request is refused. Re-authentication before a
  high-risk resolve is not in the identity module; the follow-up points at Substrate CW-20261114-0095 (design note filed by the
  Substrate driver) and CW-20261030-0082 stays open. The confirmation phrase for high and cerberus items is app code, ported
  unchanged. The elevation TTL and its `elevation-ttl` command go away with the elevation step.
- **Machine credentials: one identity key per active token, with its exact grant.** Today a grant is `{verbs, kinds}`, stored
  **per token** with the token's own expiry (`internal/agentaccess/store.go`); only a hash of each token is stored. Identity's
  tables are the module's storage, not an interface, and the program convention is re-issue; importing hashes is not done.
  - Order: the owner is created first (`identity setup`), then `access reissue`. Reissue refuses to run if any identity key
    already exists, so a second run cannot issue twice. It keys on the service identity's **id**, never its name (names are not unique).
  - At the cutover, `tachyon access reissue --out-dir DIR` creates, for each **active, unexpired** old token, one identity key
    through the identity service (`Service.CreateKey`): name = the service identity's name (a role, not a host), scopes =
    `tachyon.<verb>` for each granted verb, TTL = the token's remaining time (no TTL only where the old token had none). It writes
    each key to its own 0600 file and nothing else to output.
  - In the same transaction it records app data: `credential_id → service_identity_id`, and the credential's **kind
    restriction** (the grant's kinds). The old token row is kept, marked superseded, with the new credential id (history).
  - At request time the principal must map to a service identity; **a key principal with no mapping is refused** (`403`), so a
    key made with plain `identity key create` cannot act as an agent. `dataverbs` checks the verb scope **and** the kind list of
    the credential. Audit, attribution and `agent_launch_bindings` resolve the identity through the mapping.
  - Test (D1/B3): for every token on the rehearsal copy, the old allow-set of (verb, kind) pairs equals the new one, and expiry
    is not extended.
  - After cutover, `tachyon access issue|bind|pin-proxy|list|revoke|disable` keep their meaning, implemented on identity keys plus
    the mapping (the replacement for today's `access issue`).
  - The Tether MCP gateway's pinned identity (`service_proxy_pin`) and its `X-Tether-*` session headers are kept as app data and
    behaviour; the gateway gets a new key like every holder.
- **Approval producers.** `/api/approvals/v1` (Tether workload JWT, audience `tachyon`) is unconfigured on live (it answers 503).
  Adoption first (section 13): it is **not ported**; it becomes a post-adoption improvement. Its tables and rows migrate.
  `/api/approvals/trusted/v1` (Basalisk WebAuthn proofs) is inactive and Basalisk removed its side; **not ported** (strict-mode
  proofs, guide section 5.8), recorded in conversion notes.
- **Route scopes.** A test asserts every registered route has a scope (and one deliberately broken route fails it).

## 5. Data migration

**Input: backup files only.** `tachyon migrate-from --source DIR` reads only files produced by
`sqlite3 <db> ".backup <copy>"`. It refuses a source file that has a non-empty `-wal` beside it, and refuses a source directory
that is the configured live data directory. It opens each copy `mode=ro` only after those checks, so no uncheckpointed WAL page
can be missed. It never writes the source, and records each input file's sha256 in its report. Rehearsal and cutover use the
same kind of input: the window's step-1 backups (section 8), never the old data directory.

**Schema is enforced, not assumed.** Before copying, the command enumerates the source's `sqlite_master` (tables, indexes,
triggers, views) and `pragma table_info` of every table, and compares them with its built-in mapping. **Any table or column it
does not know is a failure, never a skip.** It also refuses a source whose `tachyon_migrations` ledger is not exactly the names
it was built for (001 to 058 since the parity-carry of 057 and 058; a source at 056 is refused with a message naming the version). Each source table has one of three rules in the mapping:
- **copy** (the default, for every table that has rows, including caches, cursors, watermarks, read models and history: they
  are copied so sync resumes where it stopped instead of re-pulling or double-delivering);
- **ledger**: `tachyon_migrations`, replaced by goose's own table;
- **drop**: only a rebuild leftover (`*_next`, `*_new`, `bus_events_055`, ...) that has zero rows; a leftover with rows fails the run.

A B2 test diffs the goose `00001_init` schema against a fresh old-tree database at 056 and against the latest rehearsal copy:
columns, declared types, NOT NULL, defaults, primary keys, foreign keys, indexes and triggers.

**Copy in SQL, not through Go values.** The target is created by goose (and `sqlstore.Migrate` for the scheduler tables) in an
empty data directory. The command then `ATTACH`es each source copy and runs `INSERT INTO main.t(cols) SELECT cols FROM src.t`
per table, in foreign-key order, inside one write transaction with foreign keys enforced at commit. Values never pass through
Go scanning, so affinity, text/integer, NULL versus empty and blobs are kept as stored. The append-only triggers on
`approval_decision_log` allow this (they block UPDATE and DELETE only). **`sqlite_sequence` is copied** for every AUTOINCREMENT
table, so ids are never reused below the old high-water mark and cursors keyed on them neither skip nor repeat.

**Storage classes.** Some columns hold values whose storage class differs from the declared type (all 1,461 `chat_events.body`
values are BLOBs in a TEXT column); the SQL copy keeps them and the `typeof()` checksum proves it.

**jobs.db.** The `gosched_*` tables (6 schedules and options, about 30k fires and fire options) are copied the same way into
the main database. The libs `core/util` version that owns this schema is v0.6.0 in both the old tree and the BOM; the command
checks the `sqlstore` schema version on both sides and fails if they differ. The scheduler module's `Migrate` at start is then a
no-op. The job type `object-sync.v1` is registered under the same name in the new build, and `migrate-from` fails on a schedule
whose job type the new build does not register. The old unit is stopped before the final backup, so no fire is in flight; any
lease row copied from a crash is past its lease and is reclaimed by the library as it would be after an old-unit restart.
Next-fire times that passed during the window are handled by each schedule's copied options, exactly as after an old-unit
restart (verified in the rehearsal by comparing the first fires). Schedule ids are a hash of a scope string that includes the
instance's upstream address; the new build's object-sync source must produce byte-identical scope strings from the cutover
config (the exact spellings are in the live facts), and `migrate-from` refuses a configured upstream that would change one.

**launches.db** (the launch-ops plugin, one empty table): not copied; left in place with the old data directory.

**Files beside the databases.** The chat workspace store (`workspace/`, the bytes behind `chat_workspace_items`) is the one
file store the old build writes. No upload has been stored so far, but slice D2 makes
`migrate-from` copy it whenever it is present: sha256 per file checked against `chat_workspace_items.sha256`, a refusal when a
row references a missing file, modes 0600/0700, and an idempotent rerun. Apart from that, nothing in the old data directory other than the databases is read by the live build: `planner/`
and `console/` are empty, and `plugins/` holds old plugin binaries that the live build does not load (it loads plugins from the
build tree). None are copied. The cache directory's `planner/` and `console/` are empty and the new cache directory starts empty.
Old snapshots and `backups/` are not copied. The verification report lists every file in the old data directory with its size,
mode and sha256 before and after, to prove it was not changed. Configuration and credential files (the app-agent config, the cache policy, token files, the
encryption key behind `approval_secrets`, the action hub's SSH key) are referenced by path from the new config, not copied;
secret files are never printed or moved (section 6). The encryption key file is a **required** setting: the new build refuses to start
without it, so `approval_secrets` rows stay readable.

**Verification, built into the command and repeated by hand in the rehearsal.**
1. Before: `PRAGMA integrity_check` = `ok` and `PRAGMA foreign_key_check` empty on each source copy; sha256 of each copy.
2. Per table, computed **in SQL on both sides**: row count; and a checksum over every column of every row including `typeof()`
   of each value (concatenated per row in primary-key order, hashed; every table has an explicit primary key today, and a
   table without one would be ordered by all its columns). Source = target or the run fails and leaves the target for
   inspection. `sqlite_sequence` is compared the same way.
3. After: `integrity_check` and `foreign_key_check` on the target; goose version = the expected head; source checksums unchanged.
4. The report (table, rule, rows, checksum; file list of the old data directory) is written beside the new database; each
   rehearsal attaches it to the Torque task.
5. Functional check (until the domain API exists, D1 compares by SQL against old-API reads; the rehearsal before P1 repeats it
   through the new API): start the new binary on the migrated copy (scratch home, upstream URLs blanked, outbound features off), sign
   in, and read one record of each major kind through the API (a planner item, an object with history, a chat thread with turns,
   an approval with its decision log, an alert, a catalog entity, a transcript). Compare with the **old binary started on a second,
   untouched `.backup` copy** (never the migrated one), with the same blanking.

**Rehearsal.** Always on fresh `.backup` copies of the live files, taken while the old unit runs, in the cell's scratch
directory, with `HOME`, the `XDG_*` variables and the data/cache directory variables pointed there. At least: one when
`migrate-from` lands (D1), one before P1, one after P1, and one on the day of the cutover, timed (it sets the window length).
Every rehearsal records counts, checksums and duration on the task.

**The isolated planner-verification instance** (a separate unit with its own data directory and blanked upstreams) is not the live app and is not migrated; it is listed for the director to retire or keep running on the old build.

## 6. Configuration

Every environment variable the old binary reads maps to a config key in `config.tachyon` or a module block, or is dropped on
purpose; the full table goes in `next/docs/conversion-notes.md` (the source list is `docs/deploy-gui-platform.md` section 2 and
appendix B). Groups:

- Listen address (`LISTEN_ADDR`, `TACHYON_ADDR`) → `httphost.addr`. Data/cache directories (`TACHYON_DATA_DIR`,
  `TACHYON_CACHE_DIR`) → resolved in `AfterConfig` (the one allowed env override), absolute paths written into `app.Config`.
- Upstreams (Tether, Tether control socket, Tether messages, Torque, Tesseract, Cerberus, Fragments Engine, activity monitor,
  events hub, Caster, another app's app-agent endpoint, GitHub org, the SSH action hub) → `tachyon.upstreams.*` addresses plus a `secrets`
  reference per credential. Credentials passed today as environment values (`TORQUE_API_TOKEN`, `GITHUB_TOKEN`) become file
  references (the operator writes the files; this cell never handles the values); the fallback to `gh auth token` is dropped.
  The Tangent approval-queue address is dropped with the bridge.
- **Outbound features** → booleans under `tachyon.features`, each defaulting to **off**: object-sync scheduler dispatch, chat
  sync, agents collector, catalog index, card events, card effects on Torque, card delivery, alert sources and the alert sink,
  transcript snapshotting, action runs. The cutover uses these (section 8). Stub switches are dropped. `TACHYON_ASSURANCE` is
  dropped (section 4).
- Policy and config files (approval producer and assurance config, cache policy, terminal policy, card delivery, app-agent
  config, the encryption key file) → path keys.

The live override file is written for the cutover from the live env file's **names** (values are carried by the operator, never
by this cell), and dry-run validated with `tachyon validate` against a scratch copy.

## 7. Clients

Each caller keeps working unchanged or has a named change and an owner. No other Hollis Labs application calls Tachyon's API
(appendix B). The callers:

| Caller | How it reaches Tachyon today | Change at cutover | Owner |
|---|---|---|---|
| The owner's browser (the SPA through the HTTPS reverse proxy) | same origin; Tangent cookie for approvals | signs in with the identity module; same URLs | owner; proxy unchanged (same port, same host) |
| Tether MCP gateway (agents' MCP calls, pinned identity) | bearer service token, `/api/agent/v1/mcp` | new key file for its identity, in the gateway's config | the director, with Tether's owner |
| Actions agents (catalog, request, await) and portfolio/planner agents | bearer service tokens, via the gateway or `/api/agent/v1/data/*` | one new key file per active token | the director, with each agent's owner |
| The app-agent client of another Hollis Labs app | bearer service token, `/api/agent/v1/mcp` | new key file | the director, with that app's owner |
| `tachyon data` (HTTP client CLI) | bearer from a token file, URL flag/env | unchanged CLI; points at a new key file | its users |
| Operator CLIs that open the database (`access`, `local-data`, `sync-objects`) | the binary on the data directory | cobra subcommands on the same boot and config, through `database.From(app)`; trust = the operator can open the data file, as for `identity setup` | this cell |
| Dropped CLIs | `import-planner`, `backfill-session-links`, `elevation-ttl`, `cmd/import-chat-history`, `cmd/import-team-cache` | one-shot importers already run, and the elevation TTL is gone; recorded in conversion notes | this cell |
| Subprocess plugins `console`, `planner` | spawned by Tachyon from the build tree | built and spawned by `pluginhost`; no external change | this cell |
| The supervisor's descriptor and the unit it writes | builds from `main` (live facts) | a release install with the override file and the new data directory (section 8) | the director (descriptor owner) |
| Dev frontends, replay and audit scripts | `/api` proxy with a cookie | rebuilt against `next/web` and identity; dev only | this cell |

Upstreams Tachyon calls do not change; only how Tachyon holds their credentials does.

## 8. Cutover sequence (run by the director with the owner)

**Before the window** (no downtime):
1. P1 done, parity-carry list empty, `main` frozen (section 1), CI green, the build artifact chosen (run id recorded). The old
   `main` commit the live app runs and its `tachyon_migrations` head are recorded; the new build's ledger list must equal that
   head (`migrate-from` refuses any other).
2. Rehearsal on that day's copies passes (section 5); duration recorded. The window's maximum length is set from it
   (rehearsal time plus 30 minutes, and never more than 90 minutes).
3. Prepared, not applied: the override file **with every outbound feature off** (section 6), with `tachyon.upstreams.tether.url`
   spelled exactly as the old `TETHER_ADDR` (chat history is keyed by that address string; a different spelling hides every
   thread) and the app-agent endpoint left unconfigured until acceptance, the new descriptor entry, the key
   list (one line per active token: identity, scopes, kinds, remaining TTL), the proxy check.
4. Every key holder named and ready to swap; **every holder keeps its old token file until acceptance** (the old tokens work
   again if the cutover is rolled back).
5. **Open, with the owner through the director:** acceptance of the loss window (rows written through the new build between
   start and acceptance, including agents' writes after the key handover, are not carried back on rollback, section 9). The
   director's recommendation is yes, provided acceptance is checked inside the window. Building does not wait on it; the
   cutover does.

**The window:**
1. Stop the old unit. Take the final `.backup` of `tachyon.db` and `jobs.db`; `integrity_check` = `ok`; record sha256. Copy the
   old binary, the old env file and the old descriptor entry aside (rollback).
2. `tachyon migrate-from --source <the step-1 backup directory>` into the **new, empty** data directory; report attached; every
   table matches.
   The cutover command is scripted without `--allow-unchecked-instances` (a rehearsal-only flag), and the report's top-level
   `allow_unchecked_instances` must read false before going on.
3. Offline bootstrap: `tachyon identity setup --username <owner>` (password on stdin), then `tachyon access reissue --out-dir
   <private dir>`. The key list is the set of tokens active at that moment. If reissue fails part-way, the recovery is a fresh
   step 2 into a new empty data directory (keys cannot be revoked through identity's Go API yet, CW-20261114-0131).
4. Apply the descriptor change (release binary, override with outbound features off, the new data directory); start the unit.
   Check that every Tether hub origin that points at Tachyon is connected again (`tether_gateway_status`); if one is marked failed,
   the owner reloads the hub (an origin that failed during a restart is not retried until a reload).
5. Section 10 checks. Hand out the key files; each holder makes one call that succeeds and confirms its old token now gets 401.
6. **Acceptance** by the director with the owner: every section 10 check passed and every holder confirmed. Only then: switch
   the outbound features on in the override, restart, and check that object sync, chat sync and the agents collector resume
   from their copied cursors (first sync run reports no full re-pull). Holders may then delete their old token files, and any copies of the old service tokens kept on the host are removed by
   the owner.
7. Merge `substrate-port` into `main` (the director's step), so the next routine deploy builds the new app.

The old data directory is never written by the new build and is left in place; the owner decides when to delete it.

## 9. Rollback

Stated before the window. **Before acceptance**, on any failed check, a holder that cannot work, or the window running past its
maximum: stop the new unit, restore the old descriptor entry and env file, start the old binary on the **old data directory,
untouched**. Holders go back to their old token files. Because outbound features were off until acceptance, the new build has
sent nothing to Torque, Tether or any other upstream, so the old app restarting on its own cursors and outbox causes no
duplicate external effects. What is lost: rows written through the new build's API during the window (the checks' own writes and
anything the owner did), which the owner accepted in advance.

**After acceptance** there is no automatic rollback: the new build has sent external effects and advanced its own cursors, and
a reverse export is not built. A later problem is fixed forward, or the director decides a recovery then. `main` is merged only
after acceptance, so a rollback never needs a revert there.

## 10. Live verification (adopting-substrate 11.6, plus Tachyon's own)

`/healthz` ok (database, scheduler, plugins); `GET /api/setup` → `setup_required: false`; sign in through the real proxy URL; one
write and one read (a planner item edit, then read back); an unknown `Host` refused; an unauthenticated `/api/` call refused,
including an unknown `/api/plugins/...` path; an unknown `/api/plugins/...` path refused for the signed-in owner too (the console
data routes are served by the app itself while `pluginhost` is deselected, section 13); an API key holding no operator scope refused on an approval resolve; each SSE
stream (console events, activity) connects and receives a keepalive; the chat thread list has the same count as the old build
reported before the window (139 at the 2026-11-15 rehearsal); the terminal WebSocket connects; the 6 object-sync schedules
listed with the same enabled state as before; one approval listed with its decision log; each key holder's one call succeeds and
its old token gets 401; the session cookie is `HttpOnly`, `Secure` and `SameSite=Strict` through the proxy; the Tether hub's
Tachyon origins are connected.

## 11. Slices (each a PR into `substrate-port`, each a Torque task)

Backend (`@mig-tachyon-builder`):
- **B1 skeleton**: `next/` module, manifest, wiring, settings (including every outbound feature switch, default off), `serve` with
  health, webui embed, CI job for `next/`, route-scope test. Agrees the API contract with the GUI track.
- **B2 store**: goose init schema (collapsed from 001–056 plus the baseline DDL, triggers kept), `database` module wiring, the
  store discipline (reads on `Reader()`, writes in `WriteTx`), the schema-diff test.
- **D1 migrate-from**: the command with its refusals, SQL copy and checksums, the report, and the first rehearsal record.
- **B3 identity and access**: identity module, assurance on identity (kind and scope), `access` on identity keys with the
  credential mapping and kind restriction, `access reissue`, the allow-set equality test, approvals without the Tangent bridge.
- **B4..Bn domains**, each with its tests ported: planner (+ planner plugin), objects and object sync (scheduler module), chat
  (threads, topics, workspace, terminal, bridge, cards), approvals, alerts, agents (collector, serve, derive, assign), forge,
  catalog and builder, fileview, transcripts, settings registry, action runs and provider actions, card delivery and events.
  Order follows dependencies (store and identity first; planner and objects before chat and forge).
- **B-cli**: kept subcommands on cobra.
- **P1 promotion** (section 1), last, then the final rehearsal.

GUI (`@mig-tachyon-gui`), against the API contract agreed in B1 (paths unchanged unless a slice says otherwise, including
`/api/plugins/{id}/...`; flat `{code, message, details?}` errors; `X-Requested-With` on writes). The new SPA is `next/web/`:
- **G1 skeleton**: `Root` around app-shell `ManifestShell` (the host owns the path, as in Parallax), `ui/app.json` with every
  surface stubbed, exact BOM pins (the kit check found no missing symbol, appendix C), `kit-identity` sign-in/sessions/sign-out
  and `kit-account` under `nav.footer`, the build and CI (manifest generation, Playwright against a `next/` binary on a scratch
  data directory).
- **G2 planner** (the landing surface), then **G3..** one slice per console area: Agent Ops, builder and registry, chat (with the
  dock tab and the PTY terminal), approvals, transcripts, repos/forge; then alerts tray, settings regions and bespoke-component
  swaps; then **G-parity**: Playwright over every surface, the deep links and legacy redirects, the narrow layout, an expired
  session and the no-owner screen.
Each GUI slice is driven in a real browser against the real `next/` backend on scratch data.

Each slice's PR states the evidence of adopting-substrate section 12 for its scope.

## 12. Appendices (from the cell's inventories, 2026-11-14; counts from `.backup` copies)

- A. Data inventory:
  - `tachyon.db`: 96 tables, 185 indexes, 2 triggers, byte-identical in schema to a fresh database at 056; integrity ok. Largest: agents_events 46,718; object_sync_runs 36,555; object_revisions 10,699;
    agents_samples 9,835; chat_turns 3,708 (with chat_publications and chat_session_links 3,708 each); object_links 3,736;
    objects 2,767; chat_turn_revisions 2,284. Source of truth includes settings, objects and links, service identities and tokens,
    approvals (79 items, 184 decision-log rows), chat (turns, reply intents, thread state, plans, topics, bookmarks, lineage,
    app-agent threads), cards (effect runs, event outbox), catalog changes, library parts, planner task meta, alert decisions.
    History: revisions, sync runs, decision log, action runs, fileview and forge audit, planner events, catalog snapshots.
    Derived but copied: caches, cursors, watermarks, chat mirror, agents read model, catalog entities, forge index, planner stream
    and ingest state, alert events. `sqlite_sequence` has 14 rows. Constraints: the decision-log triggers; the 11 baseline tables
    outside the ledger; `planner_promotions` → `object_revisions`.
  - `jobs.db`: `gosched_schedules` 6 (operator-enabled object sync), `gosched_schedule_options` 6, `gosched_fires` and
    `gosched_fire_options` about 29.7k each, `gosched_pruned` 0. One job type, `object-sync.v1`.
  - `plugins/launch-ops/launches.db`: one table, 0 rows (not copied, left in place).
  - Files: the old data directory holds old snapshots and `backups/` (history, not read by the app), empty `planner/` and
    `console/`, and old plugin binaries in `plugins/` not loaded by the live build. Configuration and credential files: listed by name and location in the live
    facts, never here.
- B. Client inventory: summarised in section 7. Auth today: browser by the Tangent bridge or the posture switch (live setting in the
  live facts), agents by bearer service tokens, the gateway by a pinned identity with `X-Tether-*` headers. The only WebSocket is
  the terminal. No other Hollis Labs application calls Tachyon. `/api/approvals/v1` is unconfigured on live; the Basalisk trusted
  route is inactive. Outbound dependencies are the upstreams of section 6.
- C. GUI inventory (summary; full survey kept by the cell):
  - Today: an npm workspace of `frontend/` (host), `packages/host-sdk`, `packages/torque-client` and `plugins/{console,planner}/ui`
    (about 66k LOC, about 112 test files), React 19, react-router 7, Vite 7, Tailwind 4. Plugins are compiled in at build time
    (runtime loading was never built). Kits are pinned per package, several to review tarballs in `third_party/`.
  - Surfaces: `/planner` (default landing; themes and lenses), `/agents`, `/builder/*` (and the legacy `/builder/classic`), `/registry`,
    `/chat` (dock and PTY over WebSocket), `/approvals` (operator elevation, terminal over WebSocket), `/dashboards/transcripts`,
    `/repos` and the PR workspace `/pr/:host/:owner/:name/:number`, `/user`, `/settings/:section`. Legacy redirects: `/`, `/tasks*`,
    `/prs/inbox`, old planner slugs, `/builder`.
  - Decisions: one SPA on app-shell; plugin UIs become surfaces compiled into it (no runtime plugin model in the UI; host-sdk shrinks
    to fetch and stream helpers); one surface per top-level area with sub-routes parsed in the page (the Parallax/Tangent pattern),
    every deep link and redirect kept; the elevate/de-elevate UI is removed and capabilities are read from the identity profile's
    scopes, the high-risk confirmation dialog stays; `/user` becomes kit-account and `/settings` becomes settings regions with
    kit-settings; bespoke components stay local unless a BOM kit now has the equivalent.
  - Kit check: every symbol Tachyon imports exists with a compatible signature in the BOM-pinned releases; the review tarballs
    are code-identical to the released versions. A scratch build on exact BOM pins typechecks, builds and passes its tests.

## 13. Gaps and decisions

Decided in the cell (reversible, recorded here): parallel `next/` module on `substrate-port`, promoted last; one database file
(jobs.db folded in); copy every table including caches and cursors; re-issue one key per active token with its exact grant and
a credential mapping; operator = a person holding `tachyon.operator`, never a key; outbound features off until acceptance; no
stdio LocalTrust; stub switches, one-shot importers, the elevation TTL and the Basalisk trusted route dropped.

Substrate gaps (Torque tasks in project Substrate, tagged `substrate-gap`, `cell:mig-tachyon`; BOM and identity questions are
coordinated with the Substrate program driver):
1. **CW-20261114-0093 (closed by Substrate #43: ui-go v0.4.0, mesh/core v0.10.0).** BOM pins older than the old tree used: `libs/mesh/core` (BOM v0.6.0, Tachyon v0.9.0) and `libs/ui/ui-go`
   (BOM v0.1.0, Tachyon v0.4.0); `libs/mcp/plugin-mcp` is newer in the BOM (v0.6.0 vs v0.3.0, an upgrade the port takes). Taken
   by the Substrate driver; until it lands, `next/` builds on the pinned versions and records any lost feature.
2. **CW-20261114-0095.** Identity step-up (re-authenticate before a high-risk action): design note, not blocking (section 4).

Director's rulings (2026-11-14, plan approved at `bea7158`):
- `main` is frozen from P1 to cutover, fixes only, each tagged parity-carry. The cell gives the director one day's notice before
  P1 (another team deploys from `main`).
- The director owns the descriptor change at cutover and names every key holder then; the cell keeps the key list (section 8)
  current.
- Pre-acceptance loss window: **open**, with the owner (section 8, before the window, step 5).
- The isolated planner-verification instance is not migrated; it stays on the old build until the director retires it.
- `/builder/classic` is ported unless the owner says otherwise (asked). Under the adoption-first guidance the cell recommends not
  porting it now.
- The stale sibling worktrees and dev descriptors of earlier Tachyon experiments are not carried.
- Live service tokens expiring before the cutover are re-issued on the live app by the director (not the port).

Decided in the cell while building (2026-11-15):
- `pluginhost` is deselected: its `* /api/plugins/` route outranks the app's own `/api/plugins/...` routes, which then answer 404.
  The app serves the console data routes itself; `pluginhost` returns with the console plugin slice once Substrate gap
  CW-20261114-0184 lands. The planner plugin process is not ported (the SPA compiles the planner UI in; post-adoption).
- On Postgres, `store.WriteTx` serialises writes with an advisory lock keyed by schema, keeping SQLite's single-writer behaviour
  the domain code relies on (PG1). The scheduler module has no Postgres store yet (CW-20261115-0014, after the three ports), so the
  Postgres step stays after acceptance.
- Ordering (with the Substrate program driver): the SQLite cutover path (domains, D2, parity, rehearsals, P1) comes before any
  further Postgres work (PG2, PG3).
- Carried UI: where a carried component overlaps a BOM kit, the kit wins and differences become kit-gap tasks. Exception: carried
  components whose kit swap would drop features in use today (chat cards undo and keyboard answers, the approvals decision bar,
  secret upload from a file, the chat composer, Sheet-over-Dialog stacking) stay as app domain UI against kit gaps
  CW-20261115-0031, -0032 and -0033, and swap on a pin bump when those ship.
- Rehearsals run with the app-agent endpoint unconfigured (with it configured, start-up polls migrated `streaming` messages).

Open, tracked: CW-20261114-0121, triage of the public-hygiene scan findings in the old tree and its history (a whole-repo scan
reports them; until it is settled, CI scans `next/` only).

Adoption first (owner guidance via the director, 2026-11-14): where the framework-standard option and an app-specific extra
compete, the port takes the simplest framework-standard path and lists the extra as a post-adoption improvement in
`next/docs/conversion-notes.md`. All data still migrates. Applied so far: `/api/approvals/v1` not ported (section 4); MCP on the
`mcp` module if the gateway can reach it (section 2); GUI surfaces use kit components where they exist and otherwise carry the
old component unchanged. Not treated as extras: the per-credential kind restriction (dropping it widens agents' grants) and the
byte-identical schedule scopes (data preservation).

## 14. Postgres readiness (amendment, 2026-11-14)

Program direction from the owner, coordinated across the in-use ports. It does not change the cutover: **the Substrate cutover
runs on SQLite as sections 5 and 8 say.** Moving data to Postgres is a separate, reversible step after acceptance, with its own
plan, copy, verification and rollback.

**Dialect-ready code.** All persistence goes through `database.From(app)`. Goose migrations are kept per dialect
(`migrations/sqlite/`, `migrations/postgres/`, the layout `database.UseMigrationFS` expects) and the Postgres set has the same
tables, columns, keys, indexes and the decision-log append-only rule (a trigger in each dialect). SQL uses `?` placeholders
through `Dialect().Rebind`. SQLite-only constructs found in the old code are replaced or isolated behind a per-dialect helper:
`AUTOINCREMENT` (identity columns in Postgres, with the sequence set from the copied high-water mark), `strftime` defaults,
`INSERT OR IGNORE`/`OR REPLACE`, JSON functions, and values stored with a different class than declared (`chat_events.body` is
declared TEXT and may hold BLOBs: it becomes `bytea` in Postgres, and the copy proves the bytes unchanged). CI runs the `next/`
tests on SQLite and on Postgres (`SUBSTRATE_TEST_POSTGRES_DSN`, the cell's scratch database); a test that cannot run on one
dialect says why.

**Table classes.** Every table belongs to one class; a test asserts the mapping covers every table in both migration sets, and no
foreign key or transaction crosses classes (today none does: every foreign key on the live schema links two tables of the same class). Cursors, watermarks and outboxes live with the data they track.

| Class | Meaning | Store | Tables |
|---|---|---|---|
| A (11 app tables + module tables) | small, local, always boots: settings, definitions, credentials | local SQLite, always | `app_settings`, `service_identities`, `service_tokens`, `service_identity_agents`, `service_proxy_pin`, `chat_approval_rules`, `console_item_state`, `library_parts`, `library_part_revisions`, `part_refs`, `tachyon_migrations`; the B3 credential-mapping table; the identity module's tables (so sign-in works when Postgres is down); all of the scheduler module's `gosched_*` tables (the module owns them in the default store; fire history is bounded by the library's own pruning with a configured retention, not moved) |
| B (28) | events, logs, telemetry, caches, replay | Postgres first | `seat_status` (057), `ceremony_daily` (059), `checkpoint_events` (059), `agents_events`, `agents_samples`, `agents_stream`, `agents_gaps`, `agents_sessions`, `agents_collector_state`, `agents_controls`, `bus_events`, `chat_events`, `chat_activity`, `alert_events`, `alert_seq`, `alert_source_cursor`, `object_sync_runs`, `fileview_audit`, `forge_audit`, `planner_events`, `planner_stream`, `planner_ingest_state`, `card_event_outbox`, `card_event_scan`, `catalog_snapshots`, `api_cache`, `provider_roster_cache`, `transcript_snapshot` |
| C (60) | domain data | Postgres later | `action_runs`, `agent_launch_bindings`, `alerts`, `app_agent_messages`, `app_agent_threads`, `approval_batches`, `approval_decision_log`, `approval_defers`, `approval_item_index`, `approval_items`, `approval_proof_receipts`, `approval_secrets`, `card_deliveries`, `card_effect_runs`, `catalog_change_paths`, `catalog_changes`, `catalog_entities`, `catalog_state`, `chat_bookmarks`, `chat_card_refs`, `chat_context`, `chat_cursors`, `chat_imports`, `chat_message_keys`, `chat_outbox`, `chat_outbox_session_links`, `chat_plan_steps`, `chat_plans`, `chat_publications`, `chat_reply_intents`, `chat_scratchpads`, `chat_session_lineage`, `chat_session_links`, `chat_sync_status`, `chat_thread_state`, `chat_topics`, `chat_turn_revisions`, `chat_turns`, `chat_workspace_items`, `execution_items`, `forge_idempotency`, `forge_index_state`, `forge_pr_index`, `forge_review_drafts`, `object_links`, `object_revisions`, `object_sources`, `object_sync_state`, `object_sync_watermarks`, `objects`, `planner_items`, `planner_promotions`, `planner_relation_index`, `planner_task_meta`, `pr_agent_links`, `provider_actions`, `provider_run_links`, `terminal_proposals`, `terminal_runs`, `terminal_sessions` |

The class mapping is kept current with the old schema: a new old migration (section 1, parity-carry) also gets a class here.
At migration 058 there are 97 tables: A 11, B 26, C 60 (058 adds a column to `chat_plans`, no table). Migration 059 (the ceremony
meter, live since 2026-11-15) adds two class-B tables: 99 tables, A 11, B 28, C 60; carried by CW-20261115-0037.

**Store topology.** At cutover all three classes are in the one SQLite database, as today. Afterwards A stays in a local SQLite
store and B, then C, move to Postgres. That needs the `database` module to give an app a second named store that may be
unreachable at start: the program's framework change CW-20261114-0151 (Substrate PR #44: `database.stores.<name>`,
`database.Named(app, name)` with `required: false`, degraded health, `database.Gate` for the 503). `stores.A(app)` is the default
store and `stores.Data(app)` the named store `data`; until #44 is available both return the default store, so the split changes
wiring, not callers. A Postgres named store does not migrate itself at start (`schema_mode: verify`): its migrations run as a deploy
step with the migration role, which PG3's runbook includes.

**When Postgres is down.** The app still boots on its A store. `/healthz` reports `degraded` with `database unavailable`; the UI
shows a banner; every data endpoint answers `503` with code `database_unavailable`; writes are refused, never queued (no durable
write spool); sign-in, settings and the health and status routes keep working. Outbound loops pause and resume when the store
returns; scheduled fires that need data fail and are retried under each schedule's own policy. A test runs the app against an
unreachable Postgres and checks all of this.

**Slices added.** PG1: the Postgres migration set, the dialect helpers and the CI matrix (after B2; it also covers every later
slice's tables; CI uses a postgres:17 service container, local runs the cell's scratch database). PG2: the class accessors, the
class-mapping test and the outage behaviour on `database.Named` (after CW-20261114-0151 lands; before it, the accessors and
tests land with PG1). PG3, after acceptance and outside this plan: the Postgres copy for B, then C,
with its own rehearsal and rollback.

