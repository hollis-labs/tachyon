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
- State machine: `draft -> awaiting_approval -> approved -> applying -> applied`, or `failed` (with
  `failure.step` validate | plan | apply | reload), `rolled_back`, `stale`, `expired`, `cancelled`. Open states
  (hold their paths): draft, awaiting_approval, approved, applying, stale. A privileged change needs
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
