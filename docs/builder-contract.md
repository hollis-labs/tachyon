# Builder + Registry contract (B0)

Final shapes for the Builder (Agents, Launches, Library) and Registry (Projects) backend:
`internal/builderapi/openapi.yaml`, fixtures in `internal/builderapi/fixtures/`, design
`tesseract:01M657C3BRSA4JJFKPKDFDS4F1` (project/tachyon/workspace/design, key
`design_tachyon_builder_registry_backend_20261030`). Run the stub with `TACHYON_BUILDER_STUBS=1`; replay the
stream with `scripts/builder-sse-replay [-l ID] [-1]`.

- Prefixes `/api/plugins/console/v1/builder` and `/api/plugins/console/v1/registry/projects`. GETs are open;
  every write (`POST /changes`, `/changes/{id}/submit|cancel|rearm`, `POST /launches/{id}/launch`,
  `PATCH /registry/projects/{id}`) needs an operator session. The real build adds operator-token assurance.
  Builder routes are not agent verbs. The design recommends gating prompt bodies and policies behind operator
  reads (F7, awaiting review): clients must tolerate 401/403 on `library/prompts|policies` and on an agent's
  `prompt`.
- Errors are `{code,message}`. `X-Stub-Status: 400|403|404|409|413|429|503` forces an error from any stub route.
  `X-Stub-Scenario`: `catalog_down` (summary shows Tether unreachable), `worker_unavailable` (launch),
  `stale_base`, `change_open`, `rate_limited` (create a change).
- **The catalog is the system of record.** Every edit is a *change* that becomes a Tether plan. Ids are slugs
  (`^[a-z0-9][a-z0-9._-]{0,63}$`); op paths address the editor model (`policy.permission_mode`), never a file.
- **Revision = sha.** Tether exposes no etag: Tachyon's `sha` (sha256 of the file) is the revision. Send it
  back as `expected_sha`. Skills have no version number, only sha and snapshot sequence.
- **Never fake zero.** `null` means not known or not reported: a down tool server has `tool_count: null`, tool
  server `scopes` are always `null` (unreadable), `summary.launches.live` is `null` when Tether is unreachable,
  `assigned_items` is always `null`.
- **Env values are write-only.** A launch carries `env.keys[]` and `secret_like_count` (names matching
  KEY|SECRET|TOKEN|PASSWORD|CREDENTIAL|AUTH). No response, diff or snapshot holds a value; diffs show
  `<value changed>`. A launch action accepts `request_id` only.
- **Capabilities** (`GET /summary`): the UI offers only what is true today. `retired` is false until Tether
  tolerates a status marker (F4): show Active/Disabled. `one_step_save` and `delegated_approval` are false.
  `tool_server_edit` is false. `resolved_by` says who computes the Resolved card.
- **Owned drafts** (`owned_draft: true`) are Tachyon-owned agents used for team formation. They list and open
  read-only; `resolved`, `checks` and `history` answer 404 `not_catalog_agent`.

## Changes (the write path)

`POST /changes {request_id, note, ops[]}` computes the edit, validates it with Tether and builds a Tether plan;
the 201 is the Review dialog payload (`diff`, `checks`, `privileged[]`, `effect`). Idempotent by `request_id`
(the stub stores nothing). Op shape: `{target:{kind,id,part_kind?}, expected_sha, op:{type,path,value,snapshot_seq}}`
with `type` one of `set | append | remove | create | restore_version | archive`.

- Deviations from the design's compressed notation: ops use a `type` key; `delete` is not offered (retire an
  agent by disabling it; archive a Library part with `archive`, refused 409 `part_in_use` while referenced);
  `restore_version` restores one file from a snapshot and never env values.
- Errors on create: 400 `invalid` | `marker_forbidden` (text containing a `tachyon:prompt` marker is refused so a
  prompt cannot forge a block); 409 `stale_base` | `change_open` (one open change per path) | `exists` |
  `part_in_use`; 413 `too_large` (prompt 64 KiB, file 256 KiB); 429 `rate_limited` (10 per minute per operator).
- State machine (direct mode, a control token is set): `draft -> awaiting_approval -> approved -> applying -> applied`, or `failed` (with
  `failure.step` validate | plan | apply | reload), `rolled_back`, `stale`, `expired`, `cancelled`. Open states
  (hold their paths): draft, awaiting_handoff, awaiting_approval, approved, applying, stale. A privileged change needs
  `ack_privileged: true` on submit (400 `ack_required`).
- **Approval gap (F3).** Tether approval needs the operator passphrase on the host, so Save is create + submit
  and then waits at `awaiting_approval`. `plan.approve_command` is the exact command to show (copy button),
  `plan.expires_at` drives the timer, and `POST /changes/{id}/rearm` re-arms the same plan after 15 minutes
  (`rearm` is an addition the design implies). Tachyon never holds or asks for the passphrase. Once Tether says
  approved, Tachyon applies automatically. Show "Saved" only at `applied`.
- **Effect.** `effect.activation` is Tether's own string, shown verbatim; never claim "immediate" unless it
  says so (a live agents-and-launches apply reported `restart_required`). `effect.running_sessions` lists the
  sessions using the touched definitions.
- **Stale.** If a base sha moved the change becomes `stale` with `{changed_paths, theirs_sha, mine_diff,
  theirs_diff}`: offer Reload (show `theirs_diff`) or Keep mine (`POST /changes` again with
  `expected_sha = theirs_sha`). Tachyon never re-plans an approved plan silently.
- `applied` carries `result {backup_id, activation, restart_required, reloaded, reload_error}`; a reload error
  is still `applied`.

## Stream

`GET /builder/stream`: flat JSON `{type, at, ...}`. `change {change_id, state}` and `catalog {observed_at,
changed_paths[], source: builder|observed}` carry `id:` and replay after `Last-Event-ID`; `summary` is a live
snapshot (after a reconnect replay cannot cover, refetch `GET /summary`). `catalog` with `source: observed`
drives the "Changed elsewhere, review" notice. 15 s keepalive.

## Cannot be fully fed (design section 11)

One-step Save; tool-server and sandbox editing (unreadable files); an authoritative Resolved card (until
Tether offers one, `resolved_by: tachyon_approx` and token counts are bytes/4 estimates); Retired (maybe);
history diffs older than Tachyon's snapshots (`diff: null`); policy "rules"; whole-catalog rollback and file
delete; Registry Machines/Services/Repos (`soon`); registry to catalog project links that fail to resolve
(`link.state: unlinked`).

Stub limits: nothing is stored; change state comes from fixtures (a submit/cancel/rearm answers with the
fixture of the resulting state, relabelled with the requested id); filters are minimal (`q`, `provider`,
`state`, `project`, `worker`, `live`).

## Change handoff (BH1, real)

Without a standing `catalog.write` credential (the default; `TACHYON_TETHER_CONTROL_TOKEN_FILE` unset) Tachyon cannot
plan or apply. The change service keeps its typed edits, diff, checks and stale detection and replaces only the
plan/apply step with a hand-off. Design: Tesseract `design_tachyon_builder_change_handoff_20261030`.

- `GET /capabilities` -> `{changes:{mode:"direct"|"handoff"|"unavailable", handoff:["copy"], applier_agent?}}`. The UI reads
  it instead of waiting for a 503. `unavailable` (503 `changes_unavailable` on the change routes) only when the read
  token is missing too.
- Handoff states: `draft -> awaiting_handoff -> [awaiting_approval] -> applied`, or `stale`, `cancelled`,
  `failed`. `POST /changes` answers `awaiting_handoff` when nothing needs an ack (a privileged change stays a `draft`
  until `submit` with `ack_privileged`, which also lands at `awaiting_handoff`). `awaiting_handoff` has no expiry.
  `change.handoff = {mode, name, bundle_sha256, files, session_available, reason?, commands:[{step:"plan"|"show"|"approve"|"apply", label, kind:"shell"|"session", command}]}` (commands are built server-side so the UI never hard-codes CLI flags; `<plan_id>` placeholder until `mark_planned`; the bundle response carries the same `commands`); each target gains `new_sha`.
- `GET /changes/{id}/bundle` -> `{name, note, files:[{path,content}], sha256}`: exactly the `POST /catalog/plans` body.
  Browser session only, `Cache-Control: no-store`. It carries full file content (may include env values): download or
  copy only, never Tesseract, mail, a prompt or a log. The UI shows `commands`: Tether's CLI has `tether catalog show <plan_id>` and `tether catalog approve <plan_id>` only
  (kind `shell`, a line to run on the host). Planning and applying are MCP tools of a session that holds `catalog.write`
  (kind `session`: `command` is a short plain instruction to give that session, e.g. "Plan this bundle with tether_catalog_plan and report the plan id" / "Apply plan <plan_id> with tether_catalog_apply and report the result"; it never embeds bundle content or env values, for `tether_catalog_plan` / `tether_catalog_apply`;
  there is no CLI for them): the UI says "Ask a session with catalog.write to plan this bundle" beside the bundle
  download/copy, and later "ask the same session to apply". An omitted `kind` is read as `shell`. A plan/apply CLI
  would be a Tether feature request. Verified against the hub CLI (`tether catalog --help`). A bundle whose files
  carry environment values (a non-empty `env`) is never handed to a session: `handoff.reason` is `contains_env_values`.
- `POST /changes/{id}/mark_planned {plan_id?}`: optional hint, `awaiting_handoff -> awaiting_approval` with
  `plan = {plan_id, state:"planned", expires_at:null, approve_command}`. Not trusted for the applied state.
- `POST /changes/{id}/check`: "Check now", re-reads the files immediately and answers the change.
- **Applied is detected, not reported.** The service compares each file's current sha with `new_sha`: all match ->
  `applied` (`result.detected: true`, `activation` and `backup_id` empty; say "Applied; check the next launch").
  A base that moved to something else -> `stale` with the usual `mine_diff`/`theirs_diff`. A partial match (some
  files applied, others still at base) stays `awaiting_*`. Detection lags the poll interval.
- Stub: handoff is the default mode; `X-Stub-Scenario: direct | unavailable` change `/capabilities` and the
  create/submit flow; on `/changes/{id}/check`, `detected` answers applied and `stale` answers stale.
  Fixtures: `chg_awaiting_handoff`, `chg_handoff_planned`, `chg_applied_detected`, `change_bundle`, `capabilities`.
- Session mode (BH2, optional) is not part of this contract yet (`handoff` lists only `copy`, `session_available` is false).
- **Real service.** `internal/builderchange` runs in handoff mode when `TACHYON_TETHER_CONTROL_TOKEN_FILE` is unset and
  the read token (`TETHER_TOKEN_FILE`) is set: Tether is wrapped read-only (`builderchange.ReadOnly`), every Tether write
  answers 403 `no_control_credential`, and `validate` is best effort (a refusal becomes a `warn` check "Not validated
  by Tether"). The 5 second poller reads each target file and compares its sha: all at `new_sha` -> `applied`
  (`result.detected`), any file at neither its base nor its `new_sha` -> `stale`, a mix of base and new keeps waiting.
  A launch action needs the control credential and answers 503 `changes_unavailable` in handoff mode, and `rearm` is
  409 (there is no Tachyon plan). With a control token the original direct flow is unchanged. With neither token the
  change routes answer 503 `changes_unavailable` and `GET /capabilities` says `unavailable`.

## Change service (B3, real)

`internal/builderchange`, mounted by `internal/server/register_builderchange.go` when the catalog index is on and
`TACHYON_TETHER_CONTROL_TOKEN_FILE` names a Tether token with `catalog.write` (never the approval passphrase; Tachyon
never approves). Without the file the routes answer 503 `changes_unavailable`. With `TACHYON_BUILDER_STUBS=1` the
stub owns the whole prefix and this service is not mounted. Routes (one pattern each, all behind `RequireOperator`):
`GET /changes[?state=]`, `GET /changes/{id}`, `POST /changes`, `POST /changes/{id}/submit|cancel|rearm`.
Migration `038_catalog_changes.sql` (take the next free number at merge).

- **Create** reads each live file from Tether, checks `expected_sha` (409 `stale_base`; required for existing files),
  applies the ops to the YAML node tree (comments, key order and unknown keys survive), diffs the *masked* old and new
  text, runs `POST /catalog/validate` with the files, then `POST /catalog/plans`. A failing validation keeps the draft
  with `fail` checks and no plan (submit then refuses with 409 `validation_failed`). Idempotent by `request_id` (a
  replay answers 200 with `X-Tachyon-Replay: 1`); one open change per path (409 `change_open`); 10 creations per
  minute per operator (429).
- **Ops** use editor-model paths only (the mapping lives in `patch.go`); `set`, `append`, `remove`, `create`,
  `restore_version`. `restore_version` takes the masked snapshot, keeps the live env values and refuses a masked value
  the live file cannot supply (400 `restore_masked`). Text containing a `tachyon:prompt` marker is 400
  `marker_forbidden`; a file over 256 KiB or text over 64 KiB is 413. Targets: agent, launch, project, skill.
  **Environment values are not writable from the Builder yet** (400 `env_write_unsupported`).
- **Submit** needs `ack_privileged` when the edit touches a privileged field or Tether's plan lists privileged files
  (400 `ack_required`). The change moves to `awaiting_approval`; `plan.approve_command` is what the operator runs on
  the host. `rearm` rebuilds the bundle on the live files and plans again (same bundle, same `plan_id`).
- **Watcher** (every 5 s): a moved base sha makes the change `stale` with `mine_diff` and `theirs_diff` and it is never
  applied; an expired plan makes it `expired`; a plan Tether reports `approved` is applied (`applying`, then `applied`
  with `result`, `rolled_back`, or `failed` with the step). An `applying` change with no answer after five minutes is
  `failed` (`interrupted`). On apply a masked snapshot (source `builder`) is stored and a `catalog` event published;
  every state change publishes a replayable `change` event on `GET /builder/stream`.
- **Not stored:** file content and environment values. The row holds the request (ops), the masked diff, checks, plan id
  and result; a re-arm recomputes the bundle from the ops.
- **Launch action** `POST /launches/{id}/launch {request_id}`: not a catalog change. Two Tether calls, `POST /sessions
  {launch, idempotency_key: "builder-launch:<request_id>"}` then `POST /sessions/{id}/launch`; a retry with the same
  `request_id` returns the same session. 201 `{session_id, agents_url:"/agents/<id>"}`. Tether's `agent_disabled`,
  `worker_unavailable`, `worker_not_configured` and `idempotency_conflict` pass through as 409 with that code; an
  unknown launch is 404. The body accepts `request_id` only (no env or override fields). Needs the `sessions` scope on
  the same control token.
- **Not in this slice:** Library `part` targets and `archive` (B5), env writes, and any new guard, audit table or step-up (posture). The approval step stays behind the host command; the
  place to swap it is `Service.Poll` reading the plan state.

### Host setup for the change service and launch action (deploy list)

1. On the Tether host create a token with the scopes `catalog.write` and `sessions` (use Tether's own token or pairing
   command; the exact command depends on the Tether version and is not shown here), and write it to a file only the
   Tachyon service user can read (mode 0600), for example `~/.config/tachyon/tether-control.token`.
2. Set `TACHYON_TETHER_CONTROL_TOKEN_FILE` to that path in the Tachyon unit's environment and restart Tachyon. The
   existing read token (`TetherTokenFile`) is unchanged and stays read-only.
   Tether's plan-write routes (`POST /catalog/plans`) answer only on its local listener. When Tachyon reads Tether
   over a remote `TETHER_ADDR`, set `TACHYON_TETHER_CONTROL_ADDR` (for example `http://127.0.0.1:8948`) so the control
   client alone goes there; a token over plain http is accepted for loopback hosts only. Unset, it uses `TETHER_ADDR`.
3. Tachyon never needs the catalog approval passphrase. The operator approves a submitted change on the host with the
   command shown in the change (`tether catalog approve <plan_id>`); Tachyon then applies it.
4. Without the variable the change and launch routes answer 503 `changes_unavailable` and everything else works.

## Library parts (B5, real)

Library **prompts** and **policies** are Tachyon-managed parts. Tether has no object for either yet (design request B4), so a part lives in Tachyon's store (`library_parts`, `library_part_revisions`) and is *compiled into* the agent files that carry it; the agent file stays the system of record.

**Changing a part** is a normal change: `POST /changes` with target `{kind:"part", part_kind:"prompt"|"policy", id}`.
- `create` with value `{name, summary, body}` (prompt) or `{name, summary, bundle}` (policy); then `set` on `name`, `summary`, `body` (prompts) or on a policy field (`permission_mode`, `network`, `default_sandbox`, `mcp_scopes`, `lifecycle`, `recovery`; `remove` unsets one); `archive`.
- `expected_sha` for a part is its revision as a string (`"3"`). A moved part is `409 stale_base`, or `stale` once the change is open.
- `archive` is `409 part_in_use` while any agent carries the part.
- Editing a part that agents carry builds **one** Tether plan with every referencing agent file recompiled, so the fleet moves together. The part row, its new revision and the references change only when that plan applies. A part nobody carries has no plan: submit applies it at once.
- Policy edits and attaches are privileged (`ack_privileged` on submit); the existing assurance interface is untouched.

**Attaching** is an op on the *agent* target:
- `append`/`remove` on `prompt.parts`, value `{"kind":"prompt","id":"<id>"}`.
- `set` (value `"<id>"`) / `remove` on `policy.part`. One policy per agent; `remove` only drops the reference and leaves the compiled fields in the file.

**Compile.** A prompt becomes a block in the agent `body`:

```
<!-- tachyon:prompt <id>@<rev> -->
<text>
<!-- /tachyon:prompt -->
```

Client text may not contain either marker (`marker_forbidden`), so text cannot forge a block; text outside blocks is never touched. A policy writes its bundle into the agent's inline fields.

**Drift.** `part_refs.compiled_hash` is the sha256 of the prompt text as it sits in the block, or of the canonical JSON of the policy fields it set. An agent is `drifted` when its file no longer hashes to that (hand edit, block removed). Drift is shown, never auto-fixed: editing the part (or re-attaching) recompiles it. Hashes are computed over the masked snapshot, so a prompt that contains a secret-looking string reads as drifted; keep secrets out of prompts.

**Read side.** `GET library/prompts|policies` (list, detail with `body`/`bundle` and `referencing_agents[{id,rev,drifted}]`, `used_by`, `history` by revision), `agents/{id}` `parts[]` and `prompt.parts`, and `summary.library.prompts|policies` / `open_changes` are real.

Migration `039_library_parts.sql` (take the next free number at merge).

**Not done:** multi-agent *rollback* of a partly applied fleet is Tether's (one plan, atomic); a policy edit does not unset a field a newer bundle dropped; bodies are not read from Tether, only from the masked index.
