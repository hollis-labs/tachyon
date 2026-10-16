# Catalog Ops

Stateless control-plane plugin over Tether's catalog (agents, launches,
projects, providers, skills, team definitions) and its agent/project registry.
Tether owns all state. Invoke with `POST /api/verb/<verb>` and a JSON body.

Configure `tether_addr` (or `TETHER_ADDR`): `unix:/path`, `tcp:host:port` or
`http(s)://host`. For a remote Tether use its `https://` Tailscale name and set
`tether_token_file` (or `TETHER_TOKEN_FILE`) to a 0600 file holding a `tth_`
token with `tether.read` plus, for the write verbs, `tether.catalog` and
`tether.registry`. The plugin refuses to pair a token with plain `http` to a
non-loopback host, never follows redirects, and re-reads the file when it is
rotated.

## Local vs remote Tether

Tether's remote listener (device token, `Tether-Protocol: 1`, sent by this
plugin) admits catalog and registry **reads**, `catalog_validate`, and
registry writes with the `maintain` scope. `catalog_plan`, `catalog_apply` and
`catalog_reload` are **local-custody routes**: over the remote listener Tether
answers 403 `route requires local custody` (checked against the hub, 2026-10-16).
They work when `tether_addr` is the daemon's own Unix socket (Tachyon on the
same host as Tether). Remote catalog writes need a Tether-side change.

## Catalog changes: plan, approve, apply

Agents, launches, projects and the rest are edited as whole YAML files in a
reviewed bundle. `catalog_plan` returns a plan id, digest, diff, validation,
privileged changes and affected running sessions; nothing live changes.
**Approval is deliberately not a verb.** Tether approves a plan only for the
local operator on its own socket with the approval passphrase, and refuses any
remote caller. `catalog_apply` installs a plan that is already approved, backs
the catalog up first and restores on failure. `activation: restart_required`
means agent, project or provider changes wait for a Tether restart; launch
changes apply at once. A GUI approval path is a Tether change (step-up) and is
tracked separately.

| Verb | Payload | Tether route |
| --- | --- | --- |
| `catalog_agents` `catalog_projects` `catalog_providers` `catalog_launches` | none | `GET /catalog/{agents,projects,providers,launches}` |
| `catalog_files` | optional `path` | `GET /catalog/files` |
| `catalog_validate` | optional `files[{path,content\|delete}]` | `POST /catalog/validate` |
| `catalog_plan` | `name`, `files`, optional `task_id`; or `rollback_of` | `POST /catalog/plans` |
| `catalog_plan_get` | `plan_id` | `GET /catalog/plans/{id}` |
| `catalog_apply` | `plan_id` | `POST /catalog/plans/{id}/apply` |
| `catalog_reload` | none | `POST /catalog/reload` |
| `catalog_backups` `catalog_audit` | audit: optional `limit` | `GET /catalog/backups`, `GET /catalog/audit` |
| `registry_search` | `kind` (`agents`\|`projects`), filters | `GET /registry/{kind}` |
| `registry_get` `registry_deregister` `registry_sync` | `kind`, `urn` | `GET`, `DELETE`, `POST …/sync` |
| `registry_register` | `kind`, `profile` (needs `display_name`) | `POST /registry/{kind}` |
| `registry_update` | `kind`, `urn`, `patch` | `PATCH /registry/{kind}/{urn}` |

Paths must be catalog-relative (no `..`, no leading `/`). `registry_update`
fills `last_updated_by` with `tachyon:catalog-ops` when the patch names no
author. Session pause/resume are not offered: Tether has no such API.

Errors carry a fixed local code. For 400, 403, 404 and 409 Tether's own message
(capped at 512 bytes) is included because it describes the caller's request, such
as a catalog validation failure; other upstream text never leaves the plugin.
