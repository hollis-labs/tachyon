# Conversion notes: old Tachyon to Tachyon on Substrate

What changes for an operator, a caller and the frontend, and what is still open. Started in slice B1; each slice adds
its rows. Plan: `docs/substrate-port-plan.md`. Contract: `docs/api-contract.md`.

## Gaps

Open items that need a decision or a change outside this cell:

1. **BOM pins (Substrate CW-20261114-0093).**
   - The old build uses `libs/mesh/core` v0.9.0 and `libs/ui/ui-go` v0.4.0; the BOM pins v0.6.0 and v0.1.0.
   - `next/` imports neither in B1, so nothing is lost yet.
   - The slice that first needs either one (Tether messaging for chat and alerts; the ui-go helpers) must use the BOM
     version or wait for the pin change, and records any lost feature here.
   - `libs/mcp/plugin-mcp` is newer in the BOM (v0.6.0 against v0.3.0); `next/` takes it through `pluginhost`.
2. **Identity step-up (Substrate CW-20261114-0095).** There is no re-authentication before a high-risk resolve. The
   operator check is kind user plus `tachyon.operator`. A design note, not blocking.
3. **Scheduler health.** `/healthz` reports `database` and `plugins`. The plan's post-cutover check also names the
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
7. **Identity keys cannot be listed or revoked through the module's Go API (Substrate CW-20261114-0131).**
   - `tachyon access reissue` reads two counts from the identity module's tables, read-only: whether an owner
     exists, and whether any key does. This is in `internal/access`, `identityCounts`.
   - `tachyon access revoke` and `disable` act on Tachyon's credential mapping. The agent API checks that mapping
     on every request, so the key and every token minted from it are refused at once. The identity module's own
     key row stays live until the module exports a revoke; a revoked key then still works on identity routes
     only within its own scopes.
8. **`GET` routes that write today** (inventory 12.1). In the port they take a write scope or become POST, decided
   per slice and recorded in the contract:
   - `access/apps?refresh=1`
   - `chat/threads/{ref}/continue`
   - terminal `proposals` and `runs` reads, which expire items

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

- **The schema is unchanged** (B2): every table, column, declared type, default, key, index and trigger of the old
  build at migration 056.
  - `chat_events.body` keeps its declared type TEXT, although existing rows may hold BLOBs.
  - The 14 AUTOINCREMENT tables keep AUTOINCREMENT.
  - The old ledger `tachyon_migrations` is kept as a table, and its rows are copied as history. The new build's
    own version table is `goose_db_version`.
- **jobs.db is not a separate file.** The scheduler module creates its `gosched_*` tables in the one database; D1
  carries the six schedules and their history.

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
