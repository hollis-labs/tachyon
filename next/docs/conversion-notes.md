# Conversion notes: old Tachyon to Tachyon on Substrate

What changes for an operator, a caller and the frontend, and what is still open. Started in slice B1; each slice adds
its rows. Plan: `docs/substrate-port-plan.md`. Contract: `docs/api-contract.md`.

## Gaps

Open items that need a decision or a change outside this cell:

1. **BOM pins: closed.** Substrate #43 raised the BOM to `libs/ui/ui-go` v0.4.0 and `libs/mesh/core` v0.10.0
   (CW-20261114-0093). `next/` is pinned to Substrate 035e0d33 (`pins sync`). Nothing was lost: `next/` uses
   neither library's older API, and the slices that bring Tether messaging and the ui-go helpers start on the new
   pins.
2. **Identity step-up (Substrate CW-20261114-0095).** There is no re-authentication before a high-risk resolve. The
   operator check is kind user plus `tachyon.operator`. A design note, not blocking.
3. **Scheduler health.** `/healthz` reports `database` (and `plugins` again once pluginhost returns). The plan's post-cutover check also names the
   scheduler; the slice that ports object sync adds a scheduler check, or files a Substrate gap if the module
   should own it.
4. **Owner questions** (from the GUI inventory, carried here until answered through the director):
   - Is the preview elevation on `tether/*` accidental? If so, drop the operator check there.
   - Should Cerberus lifecycle POSTs always need the operator check (proposed), or only behind a switch as today?
   - Is `/builder/classic` retired?
5. **Default data directory.** `XDG_DATA_HOME/tachyon-next` keeps this build away from the old build's data directory. P1
   decides the final default. The cutover names the directory in the override file in any case.
6. **Public hygiene of the old tree (CW-20261114-0121).** The CI scan covers `next/` only: `next/` at the
   commit is archived into a throwaway repository and scanned with `--no-allowlist`, and any finding fails the
   job. The old tree's and the history's findings are pinned by an allowlist on `main` (PR #702), the
   director's ruling. They never apply to `next/`.
7. **pluginhost shadows the app's own `/api/plugins/` routes (Substrate CW-20261114-0184).** It is not selected.
   The console plugin's slice needs the fix first.
8. **Identity keys cannot be listed or revoked through the module's Go API (Substrate CW-20261114-0131).**
   - `tachyon access reissue` reads two counts from the identity module's tables, read-only: whether an owner
     exists, and whether any key does. This is in `internal/access`, `identityCounts`.
   - `tachyon access revoke` and `disable` act on Tachyon's credential mapping. The agent API checks that mapping
     on every request, so the key and every token minted from it are refused at once. The identity module's own
     key row stays live until the module exports a revoke; a revoked key then still works on identity routes
     only within its own scopes.
9. **`GET` routes that write today** (inventory 12.1). In the port they take a write scope or become POST, decided
   per slice and recorded in the contract:
   - `access/apps?refresh=1`
   - `chat/threads/{ref}/continue`
   - terminal `proposals` and `runs` reads, which expire items
10. **The scheduler module has no Postgres store (Substrate CW-20261115-0014).** On Postgres the served binary does
   not start: libs ships a SQLite scheduler store only. In the Postgres CI pass the full-binary tests
   (`cmd/tachyon`) skip and cite the gap; the package tests run. The cutover is SQLite to SQLite and is unaffected.

Facts about the running deployment (addresses, paths, the exact upstream spellings the cutover override must
use) are kept out of this public repository. They are in the program's private Tesseract item
`01M7DXSRBN6J6P5CFT4KMAP0AJ`.

## Environment to configuration

The old build read the variables below (the full set found in `cmd`, `internal`, `plugins` and `pkg` on `main`
d00553e). The new build reads **no environment variable for a setting** except `TACHYON_DATA_DIR`. Each old
variable maps to a key in the `SUBSTRATE_CONFIG` override file, or is dropped on purpose.

- **Credentials.** A credential is always a secrets reference (`file://`, `keychain://`, `op://`, `helper://`,
  `env://`); a value is refused at start.
- **Features.** Every `tachyon.features.*` switch defaults to `false`. The old build's equivalents mostly defaulted
  on when the upstream was set; the cutover turns them on deliberately, after acceptance.

### Host, paths, files

| Old | New |
|---|---|
| `LISTEN_ADDR`, `TACHYON_ADDR` | `httphost.addr` (default `127.0.0.1:8093`, loopback only; the old default `:18093` on all interfaces is not carried) |
| `TACHYON_DATA_DIR` | `TACHYON_DATA_DIR` (kept: the one environment override); default `XDG_DATA_HOME/tachyon-next` |
| `TACHYON_CACHE_DIR` | `tachyon.cache_dir` (default `cache`, inside the data directory) |
| `TACHYON_PLUGIN_DIR` | dropped: plugins are the reviewed specs `pluginhost` starts (plugin slices) |
| `TACHYON_SECRET_KEY_FILE` | `tachyon.files.secret_key` (secrets reference) |
| `TACHYON_CACHE_POLICY_FILE` | `tachyon.files.cache_policy` |
| `TACHYON_TERMINAL_POLICY` | `tachyon.files.terminal_policy` |
| `TACHYON_CARD_DELIVERY_FILE` | `tachyon.files.card_delivery` |
| `TACHYON_APPROVAL_PRODUCER_CONFIG` | `tachyon.files.approval_producer` (unset: `/api/approvals/v1` answers 503, as the old build does unconfigured) |
| `TACHYON_APPROVAL_ASSURANCE_CONFIG` | `tachyon.files.approval_assurance` |

### Upstreams

| Old | New |
|---|---|
| `TETHER_ADDR`, `TETHER_TOKEN_FILE` | `tachyon.upstreams.tether.url`, `.token` |
| `TACHYON_TETHER_CONTROL_ADDR`, `TACHYON_TETHER_CONTROL_TOKEN_FILE` | `tachyon.upstreams.tether_control.url` (http(s) or `unix:/path`), `.token` |
| `TACHYON_TETHER_MESSAGE_TOKEN_FILE` | `tachyon.upstreams.tether_messages.token` (address: `tether.url`) |
| `HOLLIS_EVENTS_URL`, `HOLLIS_AUTH_TOKEN_FILE_TACHYON` | `tachyon.upstreams.events.url`, `.token` |
| `TORQUE_URL`, `TORQUE_API_TOKEN` (a value, not a file reference, in the old build) | `tachyon.upstreams.torque.url`, `.token`: a reference. The operator writes the token to a file; this cell never handles the value. |
| `TESSERACT_URL`, `TESSERACT_TOKEN_FILE` | `tachyon.upstreams.tesseract.url`, `.token` |
| `CERBERUS_ADDR`, `CERBERUS_TOKEN_FILE` | `tachyon.upstreams.cerberus.url` (http(s) or `unix:/path`), `.token` |
| `FRAGMENTS_ENGINE_URL`, `FRAGMENTS_ENGINE_TOKEN_FILE` | `tachyon.upstreams.fragments.url`, `.token` |
| `TACHYON_ACTIVITY_URL` | `tachyon.upstreams.activity.url` |
| `CASTER_URL`, `TACHYON_CASTER_TOKEN_FILE` | `tachyon.upstreams.caster.url`, `.token` |
| `TACHYON_APP_AGENT_CONFIG` (a JSON file: `url`, `token_file`, `label`, `session`) | `tachyon.upstreams.app_agent.url`, `.token`, `.label`. The file's `session` block is decided by the chat slice. |
| `GITHUB_TOKEN` / `GH_TOKEN` (a value, not a file reference, in the old build), `GITHUB_ORG` | `tachyon.upstreams.github.token` (reference), `.org`; `.url` defaults to `https://api.github.com`. The fallback to `gh auth token` is dropped. |
| `TACHYON_ACTION_HUB_SSH_ADDR`, `_USER`, `_KEY_FILE`, `_KNOWN_HOSTS` | `tachyon.upstreams.action_hub.addr`, `.user`, `.key` (reference), `.known_hosts` |
| `SSH_AUTH_SOCK` | decided by the action-runs slice (an agent socket is not a config key) |
| `TACHYON_APPROVAL_QUEUE_URL` | dropped with the Tangent bridge |
| `TACHYON_ALERTS_NOTIFY_TO`, `TACHYON_PUBLIC_URL` | `tachyon.alerts.notify_to`, `tachyon.alerts.public_url` |

### Outbound features (all default off)

| Old | New |
|---|---|
| object-sync schedules (enabled by `sync-objects --enable`) | `tachyon.features.object_sync` gates the scheduler engine; the schedules themselves are migrated data (D1) |
| `TACHYON_CHAT_SYNC_INTERVAL` (sync always on) | `tachyon.features.chat_sync`; the interval key comes with the chat slice |
| `TACHYON_AGENTS_COLLECTOR` (`0` = off) | `tachyon.features.agents_collector` |
| `TACHYON_CATALOG_INDEX` (`0` = off) | `tachyon.features.catalog_index` |
| `TACHYON_CARD_EVENTS` | `tachyon.features.card_events` |
| `TACHYON_CARD_EFFECTS_TORQUE` | `tachyon.features.card_effects_torque` |
| card delivery (on when the file was set) | `tachyon.features.card_delivery` |
| `TACHYON_ALERTS_SOURCES` (`0` = off) | `tachyon.features.alert_sources` |
| alert sink (on when `TACHYON_ALERTS_NOTIFY_TO` was set) | `tachyon.features.alert_sink` |
| `TACHYON_FORGE_INDEX` (`0` = off) | `tachyon.features.forge_index` |
| `TACHYON_PLANNER_EVENTS` | `tachyon.features.planner_events` |
| transcript snapshots | `tachyon.features.transcripts` |
| action runs (on when the hub was set) | `tachyon.features.action_runs` |

### Tuning, posture, stubs

| Old | New |
|---|---|
| `TACHYON_ACTION_DETACH_GRACE`, `TACHYON_TRANSCRIPT_MAX_SESSIONS`, `TACHYON_CACHE_TIMEOUT_MS` | keys added by the slice that ports each feature |
| `TACHYON_CERBERUS_CONTROL` | decided by the Cerberus slice |
| `TACHYON_CERBERUS_ELEVATION` | dropped: the operator check is fixed in code (owner question, Gaps 4) |
| `TACHYON_CHAT_CLIENT_CONTEXT` | decided by the chat slice |
| `TACHYON_ASSURANCE`, `TANGENT_ELEVATION_TTL` | dropped: identity replaces the Tangent bridge and elevation |
| `TACHYON_*_STUBS` (alerts, forge, planner, chat, agents, builder, fileview, approvals, settings, transcripts) | dropped from the production build |
| `TACHYON_API_URL`, `TACHYON_API_TOKEN_FILE` | client-side settings of the `tachyon data` command; kept by the CLI slice as flags |

## Data

- **The schema is unchanged** (B2, carried to 059): every table, column, declared type, default, key, index and
  trigger of the old build at its migration 059.
  - `00001_init.sql` is the old schema at 056. `00003_old_057_058.sql` replays the old 057 (`seat_status`) and 058
    (`chat_plans.position`, index changed) verbatim; `00004_old_059.sql` replays 059 (`ceremony_daily`,
    `checkpoint_events`, both class B).
  - Each later old migration on `main` is carried the same way, in its own file. `migrate-from` accepts exactly
    the ledger 001..059, and refuses a source at any other.
  - `chat_events.body` keeps its declared type TEXT, although existing rows may hold BLOBs.
  - The 15 AUTOINCREMENT tables keep AUTOINCREMENT.
  - The old ledger `tachyon_migrations` is kept as a table, and its rows are copied as history. The new build's
    own version table is `goose_db_version`.
- **jobs.db is not a separate file.** The scheduler module creates its `gosched_*` tables in the one database; D1
  carries the six schedules and their history.

## Dialects (slice PG1)

The SQLite set (`internal/store/migrations/sqlite`) is the reference and is unchanged. `migrations/postgres` holds the
same versions for Postgres; `TestPostgresSetMatchesSQLiteSet` proves both sets give the same tables, columns
(name, type class, nullability, order), keys, unique sets, indexes, foreign keys and triggers. A new migration needs
both files. Domain SQL is written once with `?` placeholders (the libs store rebinds them); what differs is below.

| Construct (SQLite) | Postgres | Where |
|---|---|---|
| `INTEGER`, `REAL`, `BLOB` | `BIGINT`, `DOUBLE PRECISION`, `BYTEA` | Postgres set |
| `INTEGER PRIMARY KEY AUTOINCREMENT` (15 tables) | `BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY`; copying data in (PG3) sets each sequence from the copied high-water mark | Postgres set |
| `strftime(...)` default (`tachyon_migrations.applied_at`) | `to_char(now() AT TIME ZONE 'UTC', ...)`, the same text format | Postgres set |
| JSON `CHECK`s (`json_valid`, `json_type`) | a `::jsonb` cast, `jsonb_typeof` | Postgres set |
| append-only `RAISE` triggers on `approval_decision_log` | one plpgsql function, the same two trigger names (`TestDecisionLogIsAppendOnly` runs on both) | Postgres set |
| a table may reference one created later | tables are created in dependency order | Postgres set |
| `chat_events.body` declared TEXT, holds bytes | `BYTEA` | Postgres set |
| column `chat_context.window` | a reserved word: quoted in the DDL, and SQL that names it must quote it (`"window"`) | Postgres set; the chat slice |
| implicit `rowid` of `object_revisions` (the planner ingest's cursor) | an explicit `row_seq` identity column | `store.RevisionSeq`, `planner/ingest.go` |
| `json_extract` | the libs dialect JSON helpers | `store.JSONInt`, `planner/derive_data.go` |
| `INSERT OR IGNORE` (5 sites) | `INSERT ... ON CONFLICT DO NOTHING`, valid on both. OR IGNORE also skipped NOT NULL and CHECK failures; at these sites only a key can conflict | `access`, `objects`, `planner` |
| a Go `bool` bound to an INTEGER column (stored as 0/1) | bind the integer | `objectsync` (`flag`) |
| one writer: read, then write, in one transaction | write transactions run concurrently, so every write takes one advisory lock (per schema) until commit: one writer at a time, and `row_seq` commits in order | `store.WriteTx` (`Writer.Submit` uses it) |

**Serialized writes are a deliberate choice** (confirmed by the program lead). The domain code reads and then writes in
one transaction (the next revision, an upsert by source key, a reorder), which SQLite's one writer makes safe. On
Postgres, `store.WriteTx` keeps that: one writer per schema, and `row_seq` in commit order.
`TestConcurrentReadThenWriteDoesNotCollide` shows concurrent read-then-write transactions do not collide on either
dialect, and fails on Postgres without the lock. Tachyon's write rate does not need more. If contention ever shows,
the later option is per-site row locks (`SELECT ... FOR UPDATE` on the row each write depends on) in place of the
one lock.

Tests: `internal/store/storetest` gives each test its own database on the suite's dialect, a temp file or, with
`SUBSTRATE_TEST_POSTGRES_DSN` set, a fresh schema (dropped after). CI runs the suite once each way. SQLite-only
tests (`migrate-from`, the old-tree schema comparison) run in both passes on their own SQLite files. The Postgres
schema mode and roles for a deployment are PG2's.

## HTTP

- Paths are unchanged, apart from the removals in the contract's section 1.
- Errors are flat `{code, message, details?}`.
- Every browser write needs `X-Requested-With`.
- `/api/plugins/` forwards only allowlisted paths.

Details: `docs/api-contract.md`.

## Agents and credentials (slice B3)

- **Old tokens become identity keys.**
  - `tachyon access reissue --out-dir DIR` runs once, after `tachyon identity setup` and before any identity key
    exists.
  - It turns every active old service token into one identity key: keyed on the service identity's id, with
    exactly the old grant, and a lifetime of the token's remaining time less one second.
  - It writes each key to its own 0600 file, `<identity id>.<credential id>.key`, and prints only ids and
    counts.
  - Before handing anything out, it proves for each key that the old allow-set of (verb, kind) equals the new
    one, and that the expiry is not later. If any check fails, every key it made is revoked and no file is
    written.
  - Old token rows are kept and marked superseded (`service_credentials.source_token_id`).
  - A reissue that fails after creating keys revokes their credentials, but the identity module keeps its key
    rows (no exported revoke, Gaps 7). It therefore refuses to run again on that data directory; the window
    restarts from `migrate-from` on a new one.
- **Holders swap their token file for the key file.**
  - The bearer is the same kind of header; the value changes.
  - A key never counts as a person: no key, whatever its scopes, passes an approvals assurance check.
- **The `tachyon access` commands keep their names, now on identity keys:** `issue` (`--out FILE` replaces
  `--token-file`), `bind`, `unbind`, `pin-proxy`, `unpin-proxy`, `list`, `revoke` (a credential id), `disable`
  (an identity id).
  - `access` never grants `*` or `tachyon.operator`.
- **Assurance** comes from the signed-in person, with no elevation step:
  - browser-session: any signed-in user;
  - operator-token: a signed-in user holding `tachyon.operator` (the owner, through `*`).

  The Tangent session bridge, `/approval-inbox/session`, `/status`, `/elevate` and `/de-elevate`, and the elevation
  TTL are gone. The confirm phrase stays in the approvals code.

## Objects and object sync (slice B4)

- **Same API.** The objects data verbs and the sync status route keep their paths and semantics: the agent API with
  grants and kinds, and the console API with scopes.
- **`tachyon sync-objects --status|--enable|--disable [--source NAME] [--interval D]`** behaves as before. It is
  now a cobra command on the data directory, and `--status` lists every schedule (id, type, enabled, next run),
  whether or not its source is configured here.
- **The schedules are the migrated ones.** With the feature on, each due schedule fires once, its missed runs
  coalesced (`run_once`), under its copied id. A schedule whose source is not configured in this process is
  skipped and left as it is (the old rule). `tachyon.features.object_sync` gates the engine; with it off nothing
  fires.
- **Upstream credentials** are secrets references resolved per request (`upstreamauth`). A rotated secret is picked
  up without a restart. GitHub's token is no longer a value: `tachyon.upstreams.github.token` is a reference.

## Planner (slice B5)

- **Same API.** The planner API keeps its paths, legacy prefix and semantics, compared with the old build on copies
  of the same data.
- **`TACHYON_PLANNER_EVENTS` is now `tachyon.features.planner_events`** (default off). Off means the ingest worker
  (Torque task moves and merged PRs into the timeline) does not run. Planner object revisions still reach the
  stream, because the stream tails them locally.
- **The planner plugin process is not ported.** It only registered the UI's name; the SPA now compiles the planner
  UI in. Its data was always served by the host. **Post-adoption:** if it comes back, it returns as a
  `pluginhost` spec once CW-20261114-0184 is fixed. `import-planner` is dropped, as planned.
- **`pluginhost` is not selected** (contract section 6; Substrate CW-20261114-0184).
- **Torque's credential** for the planner is `tachyon.upstreams.torque.token`, a secrets reference resolved per
  request.

## Chat without threads (slice B6)

- **Same API** for topics, bookmarks, plans (several per thread, from `main`'s #725), steps, workspace, scratchpad,
  settings, continue, activity, context and the Work view (`docs/api-contract.md` section 5c). Compared with the
  old build's handlers on copies of the same data.
- **Threads are B6b** (CW-20261115-0025): thread list and detail, messages, send, read, stop and both event streams,
  over the chat bridge's inbound side and the app agent. Until then:
  - the thread-existence check reads the local tables (`chat.LocalThreads`);
  - a topic's `message_ref` is not resolved, so its `seq` is null;
  - changes are not streamed.
- **`tachyon.workspace_dir`** (default `workspace`, anchored in the data directory) holds the chat workspace
  files, the old build's `$TACHYON_DATA_DIR/workspace`. The old layout is refused there too.
- **Workspace files and migrate-from.** `migrate-from` copies the databases, not the old `workspace/` files. The
  2026-11-14 snapshot has no workspace items, so nothing is lost today. If files exist at cutover, they must be
  copied into `tachyon.workspace_dir` (open: raised with the program lead).
- **Torque reads** (`internal/torqueread`, used by the Work view): the token is a secrets reference resolved per
  request, as for the planner.
- **Not ported:** the chat dev stub (`TACHYON_CHAT_STUBS`) with its OpenAPI document and fixtures, as for the
  planner.

## Web UI: carried console code (G3a, G3)

The console surfaces' code is carried from the old tree (`plugins/console/ui/src` and `packages/*/src` at main
4a76422) into `next/web/src/console` and `next/web/src/packages`, unchanged apart from the edits each PR lists
(`next/web/CONVERSION_NOTES.md`).

**Carried as app domain UI; reviewed against kits in G3.** The rule: where carried code overlaps a BOM kit, the kit
wins, and each real difference is a kit-gap task rather than a reason to keep the old component.

| Directory | What it is | Kit review (G3) |
|---|---|---|
| `src/console/chat` | conversation list, thread view, aside panels, drawer, workbench | already on kit-chat `ChatStream`, `ChatMarkdown`, `ForwardMessageDialog`. Composer and `chat/cards` stay app UI until kit gaps CW-20261115-0032 (kit-chat) and CW-20261115-0031 (kit-cards) land, because swapping now drops live features (undo window, keyboard answers, attachment strip, `/` palette). `RunCard`, `ToolCallsRow`, `MailWakeRow`: no kit equivalent |
| `src/console/messaging` | mail model, composer, quick actions | **swapped:** `QuickActions` now uses design-components `Popover` and `Dialog` |
| `src/console/autolink`, `src/console/refs` | reference chips, detection, previews, drawer | `sheet.tsx` stays (Sheet-over-Dialog stacking, CW-20261115-0033) |
| `src/console/forge`, `events`, `observations`, `builder`, `agents`, `files`, `fleet`, `tesseract` | the preview panes reference chips open | domain views on design-components and kit-dashboard parts; `files` table is rendered file content, not a data table |
| `src/console/approvals`, `components` (G4) | approvals surface; chip, avatar, keycap, pill tabs | see G4: `PillTabs` swaps to `DashboardViewTabs`. The decision bar and `CardForm`/`SecretDialog` stay app UI until CW-20261115-0031 (undo, snooze, secret upload). `InitialsAvatar` and `Keycap` have no kit equivalent |
| `src/console/agents-monitor`, `needs-you`, `fleet` launch (G5, main 99c7b88) | Agent Ops (sessions, timeline, seats, ceremony, Teams, controls, launch) and Needs you | domain views on design-components and kit-dashboard layout; no kit equivalent |
| `src/console/builder`, `registry`, the rest of `agents` (G6, main 4b5e397) | Builder (agents, launches, library, team definitions), Registry, and team operations in the Agent Ops Teams tab | **swapped:** `Chip` to design-components `Pill`. Editors and forms are builder domain views on design-components; `/builder/classic` not ported |
| `src/console/transcripts` (G7a, main d5db765) | the transcript list and reader (read-only) | domain view on design-components; no kit equivalent. The operator-session Connect step is gone (no elevation) |
| `src/console/forge` inbox and workspace (G7b, main d5db765) | Repos: the PR inbox and the PR workspace (diff, conversation, review, merge, assign) | **swapped:** `Chip` to `Pill`, `PillTabs` to `DashboardViewTabs`. Comment, review and Assign stay refused as in the old app (no step-up served); enabling them is post-adoption |
| `src/console/{lib,seams,shell,operator,testkit}` | helpers, seams, test kit | `operator/elevation.ts` is replaced by the port (no step-up) |
| `src/packages/torque-client`, `src/packages/host-sdk` | API client, host fetch and freshness | `hostFetch` sends `X-Requested-With` and hands a 401 to sign-in |

**Kept until a kit gap ships.** These stay as app domain UI. Each one is swapped for the kit on the pin bump that
ships its gap, and nothing new is built on them.

| Kept | Kit gap | Swap |
|---|---|---|
| `chat/cards` (approval, question, plan, dispatch, notice cards) | CW-20261115-0031 (kit-cards) | swap on pin bump when the gap ships |
| approvals decision bar (`approvals/detail.tsx`, `batch-panel.tsx`) | CW-20261115-0031 (kit-cards) | swap on pin bump when the gap ships |
| `CardForm` and `SecretDialog` (to kit-cards `FormCard`) | CW-20261115-0031 (kit-cards) | swap on pin bump when the gap ships |
| chat `Composer` (`chat/workbench/Composer.tsx`) | CW-20261115-0032 (kit-chat) | swap on pin bump when the gap ships |
| autolink `sheet.tsx` (Sheet over Dialog); its acceptance test is `autolink/drawerStacking.test.tsx` | CW-20261115-0033 | swap on pin bump when the gap ships |
| page heading (the shell's and the page's `h1`) | CW-20261115-0026 (app-shell `PageHeader`) | drop the extra heading on pin bump when the gap ships |

Main has moved since 4a76422 under this layer (`chat/MessageBody.tsx`, `files/FileViewerPane.tsx`, `observations/store.ts`, `chat/aside/FileTree.tsx`, `chat/aside/Panels.tsx`). A sync slice
before P1 picks that up.

## What the operator does differently

- **First run.** Create the owner offline before the first start:
  `printf '%s\n' "$PASSWORD" | tachyon identity setup --username <owner>`. Setup over HTTP is refused.
- **Configuration.** One override file, named by `SUBSTRATE_CONFIG`, replaces the env file. Every credential is a
  reference to a file (or keychain entry) that the operator writes.
- **Data.** A new data directory. The old one is never opened by this build; its data is copied once:
  1. Stop the old unit.
  2. `sqlite3 <old>/tachyon.db ".backup DIR/tachyon.db"`, and the same for `jobs.db`.
  3. `TACHYON_DATA_DIR=<new, empty> tachyon migrate-from --source DIR` (exit 0, report beside the new database).
  4. `tachyon identity setup`.

  `launches.db`, `backups/`, `plugins/` and old snapshots stay with the old directory.
- **Upstream addresses must keep their exact form.** Write the Tether, GitHub and Cerberus upstream addresses in
  the override exactly as the old schedules' scopes record them (no trailing slash, the same socket path).
  - Object sync derives schedule ids from those strings.
  - `migrate-from` refuses an upstream that differs, and one that is not configured at all.
  - `--allow-unchecked-instances` is for rehearsals with blank upstreams, never for the cutover.
