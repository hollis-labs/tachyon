# Deploying and testing the GUI platform work

One page for the deploy of the merged GUI-platform backends (Approvals, Alerts, Builder, Chat, File viewer, Forge, Agents, Transcripts,
Settings). Facts are from `origin/main` at the time of writing; re-check flags with `grep -rn 'TACHYON_' cmd internal` before you rely on
this list. Per-feature detail lives in the `docs/*-contract.md` and feature pages linked below. Backend 2's earlier smoke notes:
Tesseract `tachyon_deploy_smoke_20261030` (project/agent-os/workspace/portfolio-manager). Posture is relaxed: nothing here adds guards.

## 1. Build, back up, restart

1. **Back up first.** The database is `$TACHYON_DATA_DIR/tachyon.db` (default `~/.local/share/.../tachyon.db`; look in the data dir the
   service uses). Take a consistent copy before the new binary starts: `sqlite3 tachyon.db ".backup tachyon.db.pre-gui"` and check
   `sqlite3 tachyon.db.pre-gui "pragma integrity_check"` prints `ok`.
2. **Build:** `make all` (frontend into `internal/webui/dist`, plugin binaries into `bin/plugins`, then `./tachyon`). A host build without
   the UI needs a placeholder `internal/webui/dist/index.html`, otherwise `go vet ./internal/server` fails to embed.
3. **Restart** through Cerberus (`tachyon.cerberus.yaml`: systemd user unit `tachyon`, run from the checkout so `bin/plugins` resolves).
4. **Migrations run on start, in order, and are additive.** Expect 028 to 040 on a database that stopped at 027 (Backend 2's copy of the
   live DB went 18 to 39 cleanly and backfilled the approval index). Migrations are never edited after they merge; a failed one stops start.

| # | Table(s) | Feature |
|---|---|---|
| 028 | approval index | Approvals A1 (backfilled from existing items on first start) |
| 029 | defer log | Approvals defer/history |
| 030 | catalog index | Builder B1 |
| 031 | console item state | `/console/state` (read, snooze, dismiss) |
| 032 | alerts, alert_events, alert_seq, alert_source_cursor | Alerts |
| 033 | fileview audit | File viewer |
| 034 | approval secrets | Approvals secrets (needs a key, below) |
| 035 | forge index | Forge pull requests |
| 036 | approval_batches | Batch approve |
| 037 | chat_card_refs | Chat cards bound to threads |
| 038 | catalog_changes | Builder B3 change service |
| 039 | library_parts, part_refs | Builder B5 |
| 040 | chat_approval_rules | Chat "Always in this chat" rules |

## 2. Environment, flags and token files

Everything is optional unless noted. Unset means the default in the last column.

| Variable | Feature | Effect when set | Unset |
|---|---|---|---|
| `TACHYON_DATA_DIR`, `TACHYON_CACHE_DIR`, `TACHYON_PLUGIN_DIR` | host | where the DB, caches and plugin binaries live | XDG dirs, `bin/plugins` |
| `TACHYON_ADDR` / `LISTEN_ADDR` | host | listen address | `:18093` |
| `TACHYON_APPROVAL_QUEUE_URL` | Approvals | upstream queue | `http://127.0.0.1:7843` |
| `TACHYON_APPROVAL_PRODUCER_CONFIG`, `TACHYON_APPROVAL_ASSURANCE_CONFIG` | Approvals | producer trust file; assurance table override | defaults |
| `TACHYON_SECRET_KEY_FILE` | Approvals secrets | 32 raw bytes or 64 hex chars, owner-only | secret submit answers 503 `secrets_unavailable` |
| `TACHYON_CARD_EFFECTS_TORQUE=1` | Cards | approved cards may change Torque (status, and create via `torque:task/new`) | card effects to Torque are refused |
| `TACHYON_CARD_DELIVERY_FILE` | Cards | push outcomes to the asker (`docs/card-delivery.md`) | pull only (`card.await`) |
| `TACHYON_CARD_EVENTS=1`, `HOLLIS_EVENTS_URL`, `HOLLIS_AUTH_TOKEN_FILE_TACHYON` | Cards | publish card events | off |
| `TACHYON_ALERTS_SOURCES=0` | Alerts | leaves all alert adapters off | adapters run (activity, Agents bus, Torque events, Tether poll, approvals emitter) |
| `TACHYON_ACTIVITY_URL` | Alerts | activity monitor stream | `http://127.0.0.1:8099` |
| `TACHYON_FORGE_INDEX=0` | Forge | index and poller off | on |
| `GITHUB_TOKEN` / `GH_TOKEN`, else `gh auth token`; `GITHUB_ORG` | Forge | poller identity and org | org `hollis-labs`; no token means saved rows only, `GET /status` says so |
| `TETHER_ADDR`, `TETHER_TOKEN_FILE` (read scope) | Agents, Builder reads, Alerts Tether poll | collector and catalog index | those features stay off |
| `TACHYON_AGENTS_COLLECTOR=0`, `TACHYON_CATALOG_INDEX=0` | Agents, Builder | turn the collector or catalog index off | on when Tether is set |
| `TACHYON_TETHER_CONTROL_TOKEN_FILE` | Builder changes (direct mode) | a Tether token with `catalog.write` and sessions scope, never the approval passphrase | handoff mode on the read token (see below); `unavailable` with no read token either |
| `TACHYON_TETHER_CONTROL_ADDR` (optional) | Builder changes | where the change service reaches Tether, for example `http://127.0.0.1:8948`; used only by that client, reads keep `TETHER_ADDR` | `TETHER_ADDR` |
| `TACHYON_CASTER_TOKEN_FILE`, `CASTER_URL` | File viewer V5 | read-scoped device token from `caster pair -scope read -label tachyon` | `caster://` refs answer 501 |
| `TACHYON_APP_AGENT_CONFIG`, `TACHYON_CHAT_CLIENT_CONTEXT=1`, `TACHYON_CHAT_SYNC_INTERVAL` | Chat | Nanite config for the app agent; forward client context; sync period | app agent not configured; off; `5s` |
| `TORQUE_URL`, `TORQUE_API_TOKEN` | Planner, Work, cards, Alerts | Torque base URL and token | `http://127.0.0.1:8990`, no token |
| `FRAGMENTS_ENGINE_URL`, `FRAGMENTS_ENGINE_TOKEN_FILE`, `TESSERACT_URL`, `TESSERACT_TOKEN_FILE`, `CERBERUS_ADDR`, `CERBERUS_TOKEN_FILE` | various reads | upstream addresses | those reads are off |
| `TACHYON_TRANSCRIPT_MAX_SESSIONS` | Transcripts | session cap | `5000` |
| `TACHYON_ACTION_HUB_SSH_ADDR`, `_USER`, `_KEY_FILE`, `_KNOWN_HOSTS`, `TACHYON_ACTION_DETACH_GRACE` | Approval actions | SSH hub for run actions | no hub |

### Credentials per app (`GET /api/plugins/console/v1/access/apps`)

Tachyon is the operator: it should hold full read and write on every app it talks to. The route answers, per app, the base URL, whether a credential is `configured`, `missing` or `rejected` (a refused read means the token itself is wrong), a `read_probe` and a `write_probe` (`ok`, `denied`, `unreachable`, `not_probed`). A `denied` write with an `ok` read is a missing write scope. `?refresh=1` skips the 30 s cache. Behind the operator guard; tokens are never returned. Probes are harmless: a write probe only exists where the app can answer "no such thing" without creating anything (Torque: a comment on a task that does not exist). Authorization is read from HTTP 401/403 only.

| App | Base URL variable | Credential variable | Needs | Read probe | Write probe |
|---|---|---|---|---|---|
| Tether | `TETHER_ADDR` | `TETHER_TOKEN_FILE` (builder changes: `TACHYON_TETHER_CONTROL_TOKEN_FILE`, address `TACHYON_TETHER_CONTROL_ADDR`; forge assign: `TACHYON_TETHER_MESSAGE_TOKEN_FILE`) | read scope; control token adds `catalog.write` and sessions | `GET /catalog/history` | not probed |
| Torque | `TORQUE_URL` | `TORQUE_API_TOKEN` (value, not a file) | read and write on tasks, comments and the other resources | `GET /api/v1/tasks` | `POST` comment on a task id that does not exist |
| Tesseract | `TESSERACT_URL` | `TESSERACT_TOKEN_FILE` | read; writes create items | `GET /v1/items/{id}` | not probed |
| Cerberus | `CERBERUS_ADDR` | `CERBERUS_TOKEN_FILE` | read | `GET /hosts` | not probed |
| Nanite (app agent) | `url` in the file named by `TACHYON_APP_AGENT_CONFIG` | `token_file` in that file | `operate`+`tools` client credential | `GET /api/agent/v1/sessions` | not probed |
| Fragments Engine | `FRAGMENTS_ENGINE_URL` | `FRAGMENTS_ENGINE_TOKEN_FILE` | read | `GET /v1/transcripts/status` | not probed |
| Forge (GitHub) | `GITHUB_ORG` | `GITHUB_TOKEN` / `GH_TOKEN`, else `gh auth token` | repo read; PR comments for forge writes | `GET /rate_limit` | not probed |
| Caster (file viewer) | `CASTER_URL` | `TACHYON_CASTER_TOKEN_FILE` | read-scoped device token | not in the route | not in the route |
| Cards events | `HOLLIS_EVENTS_URL` | `HOLLIS_AUTH_TOKEN_FILE_TACHYON` | publish | not in the route | not in the route |
| Tangent | none | none | Tachyon has no Tangent integration | not probed | not probed |

Token files must be mode 0600 and are refused over plain http to a non-loopback host.

**Stub flags.** `TACHYON_ALERTS_STUBS`, `TACHYON_FORGE_STUBS`, `TACHYON_PLANNER_STUBS`, `TACHYON_CHAT_STUBS`, `TACHYON_AGENTS_STUBS`,
`TACHYON_BUILDER_STUBS`, `TACHYON_FILEVIEW_STUBS`, `TACHYON_APPROVALS_STUBS`, `TACHYON_SETTINGS_STUBS`, `TACHYON_TRANSCRIPTS_STUBS`
replace that feature's real routes with fixtures. **For the deploy none of them should be set to 1.** Transcripts has no real backend yet
(TR2 onward is not built), so `TACHYON_TRANSCRIPTS_STUBS=1` is the only way those routes exist.

## 3. What is inert until configured

- Builder changes: without `TACHYON_TETHER_CONTROL_TOKEN_FILE` they run in **handoff mode** on the read token. A change stops at `awaiting_handoff` with a bundle (`GET builder/changes/{id}/bundle`) a session with `catalog.write` plans in Tether (MCP `tether_catalog_plan`; the CLI has no plan/apply), the operator runs `tether catalog show/approve <plan_id>` on the host, and the session applies (`tether_catalog_apply`); Tachyon detects the apply from file shas. `GET builder/capabilities` reports `direct`, `handoff` or `unavailable` (no read token). Reads always work from the catalog index.
- Builder reads and the Agents collector: off without `TETHER_ADDR`/`TETHER_TOKEN_FILE` (a token is refused over plain http to a non-loopback host).
- File viewer `caster://`: 501 without the caster token file. Local files work without it.
- Secret submit: 503 without `TACHYON_SECRET_KEY_FILE`.
- Torque effects of approved cards: refused without `TACHYON_CARD_EFFECTS_TORQUE=1`.
- Forge: serves saved rows only without a GitHub token.
- Chat runtime tool approvals: only act if the app agent's Nanite sends `approval.request` (chatstream/v1); until verified deployed, a tool call that needs approval is handled by Nanite as before. Rules and cards exist but nothing creates them otherwise.
- Alerts outbound sinks (AL7) do not exist. Planner real routes stand down for `TACHYON_PLANNER_STUBS=1`; the planner assignment model (PL1) is still held.

## 4. Post-deploy checks (no session needed)

Base `B=https://<host>/api/plugins/console/v1`. A browser session is not needed for these reads; use `curl -s -o /dev/null -w '%{http_code}'`.

| Route | Expect | Meaning of other codes |
|---|---|---|
| `GET $B/alerts`, `/alerts/counts`, `/alerts/prefs`, `/alerts/sources`, `/alerts/rules` | 200 | 403 means an `Authorization` header was sent (alerts take browser sessions only) |
| `GET $B/alerts/stream?once=1` | 200 `text/event-stream` | |
| `GET $B/console/state` | 200 `{data}` | |
| `GET $B/builder/summary`, `/builder/agents`, `/builder/library/{kind}`, `/builder/launches` | 200 | 503 `registry_unavailable`/index off: Tether not configured |
| `GET $B/builder/changes` | 200 with the token file, **503 without** | |
| `GET $B/forge/pulls/pending`, `/forge/status`, `/forge/viewer` (see `docs/forge-index.md`) | 200 | `status` shows token owner, scopes, rate limit |
| `GET $B/chat/settings`, `/chat/threads` | 200 | |
| `GET $B/chat/threads/{ref}/cards`, `/approval-rules`, `/work` | 200 (404 for an unknown thread) | |
| `GET $B/files`, `/approval-inbox`, `/account` | **401** without an operator session | 401 here is correct |

## 5. Manual tests that need an operator session

Sign in the usual way (operator token; the cookie is `tachyon_approvals`). Then:

- **Approvals.** Open items arrive from askers; test a low item, a medium one, and a high or destructive one. High and destructive need
  `response.confirmation` equal to the item's `confirm.phrase` (else 422 `confirmation_required`). Batch: select several low items and
  approve; results are per item (`resolved`, `stale`, `ineligible`, `expired`, `conflict`, `error`); re-sending the same `batch_id`
  replays (`X-Tachyon-Batch-Replay: 1`). Check undo (the result bar's undo warning) and that Cerberus-sourced items report
  `risk_not_low` in a batch. Details: `docs/approvals-batch.md`, `docs/approvals-contract.md`, `docs/approvals-assurance.md`.
- **File viewer.** `TACHYON_COOKIE='tachyon_approvals=...' scripts/fileview-curl-matrix -b <base> -r <dir under a served root>` on the host;
  prints PASS or FAIL per case and exits with the number of failures. The matrix does not cover V5: pair the caster
  (`caster pair -scope read -label tachyon`), write the token to a file, set `TACHYON_CASTER_TOKEN_FILE`, restart, open a `caster://` ref.
- **Builder against live Tether.** With the control token set: edit a field in an agent, Save (create plus submit). It becomes
  `awaiting_approval` and shows `tether catalog approve <plan_id>`; run that on the host, then watch it reach `applied`. Show the plan's
  activation text as given. Try a stale base (edit the file under it) and expect `stale`. Cancel a draft. Confirm one real
  validate plus plan call early; Tether's activation semantics are not fully verified.
- **Alerts server mode.** The shell always uses the server source. Check the bell, tray, snooze, defer, settle, bulk read, and that a new approval item raises an alert and settles when decided.
  `GET /alerts/sources` shows each adapter `ok`, `degraded` or `down`.
- **Chat cards.** In an app thread have the agent call `card.ask` with `thread_ref` set: the card appears in the thread and in
  `GET /chat/threads/{ref}/cards`; answer it in Approvals and check the agent receives "Card answered: ...". With
  `TACHYON_CARD_EFFECTS_TORQUE=1`, approve a `card.dispatch` whose effect is `{target:"torque:task/new", op:"create", value:"<title>"}`
  and check the new task id in the receipt. Tool approvals ("Always in this chat") need a Nanite that sends `approval.request`; verify
  the frame shape once (`approval_id`, `call_id`, `descriptor{tool,input,class}`).
- **Planner and console plugins.** Plugin routes need `make plugins` first. Planner real routes need a real Planner model (PL1 is held).

## 6. Rollback

1. Stop the service; restore the pre-deploy binary.
2. Restore `tachyon.db.pre-gui` over `tachyon.db` (also remove `-wal` and `-shm` files). Migrations 028 to 040 are additive and old binaries
   ignore the new tables, but restoring the backup is the clean path, and it drops any alert, card, batch or change rows made since.
3. To keep the new binary and switch a feature off instead: `TACHYON_ALERTS_SOURCES=0`, `TACHYON_FORGE_INDEX=0`,
   `TACHYON_CATALOG_INDEX=0`, `TACHYON_AGENTS_COLLECTOR=0`, unset `TACHYON_TETHER_CONTROL_TOKEN_FILE`, `TACHYON_CASTER_TOKEN_FILE` and
   `TACHYON_CARD_EFFECTS_TORQUE`; or set that feature's `*_STUBS=1` to serve fixtures.
4. A Builder change that reached `applied` has already changed Tether's catalog. Roll it back with `tether catalog` (the plan's
   `backup_id`, or a `rollback_of` plan); Tachyon's rollback does not undo it.
