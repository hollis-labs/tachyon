# Settings, Account and Assurance: running it, curl matrix, security notes (ST6)

Shapes and per-slice detail are in `docs/settings-contract.md` and `internal/settingsapi/openapi.yaml`. Design:
`tesseract:01M65GDQC0KC87CMT6CJZTFFD2`. This page is the operator's view.

## What runs

| Part | Where | Switch |
|---|---|---|
| Admin manifest and group routes (registry, layered values) | `internal/settingsreg`, `register_settingsreg.go` | `TACHYON_SETTINGS_STUBS=1` serves the in-memory stub instead |
| Real groups: `general` (`elevation_ttl`), `chat` (`scratchpad_placement`), `agents` (stale, handoff, nudge presets), `transcripts` (read-only, env) | `register_settingsgroups.go` | none |
| `GET /account`, `PUT /account/profile` | `internal/account`, `register_account.go` | stands down under the settings stub |
| `/approval-inbox/access` extra fields, `/access/challenge`, `/access/verify` | approvals inbox + `assurance.Stepper` | challenge/verify answer 501 `unsupported` until a Provider runs the exchange |

## Behaviour to know

- Value precedence is override, env, file, default (a group may change it). Only the override layer is written, to `app_settings` key `settings.<group>`. Env and file values are read, never written.
- Every value reports `source`, `has_override`, `editable` and `apply_state`. Env-backed groups are read-only. `elevation_ttl` is `pending_restart` (target `tangent`) and needs an operator-token session.
- Update and reset need `If-Match` with the ETag of the last read. Stale ETag: 412 `value_conflict`; manifest changed: 409 `manifest_changed`; invalid value: 422 `validation_failed`; missing header: 428 `precondition_required`.
- The profile is one local row (`account.profile`: display name 1-80, optional email). `identity.assurance` is `verified` only while the session is elevated. The profile is never an authorization input.
- Lockout (`access.lockout`) is an in-process estimate of one minute after Tangent answers 429; it resets on restart.

## Curl matrix

Browser session only; use the cookie jar (`-b`). Writes need a same-origin `Origin`.

```sh
H=https://tachyon.example
A=$H/api/plugins/console/v1/admin
O="Origin: $H"

curl -s -b jar "$A/manifest"                                     # groups, fields, endpoints
curl -si -b jar "$A/settings/agents"                             # values + ETag header
curl -s -b jar -H "$O" -H 'Content-Type: application/json' -H 'If-Match: "<etag>"' \
  -d '{"revision":"<revision>","set":{"stale_after_s":900}}' "$A/settings/agents/update"
curl -s -b jar -H "$O" -H 'Content-Type: application/json' \
  -d '{"revision":"<revision>","set":{"stale_after_s":5}}' "$A/settings/agents/validate"   # dry run, no write
curl -s -b jar -H "$O" -H 'Content-Type: application/json' -H 'If-Match: "<etag>"' \
  -d '{"revision":"<revision>","keys":["stale_after_s"]}' "$A/settings/agents/reset"
curl -s -b jar "$H/api/plugins/console/v1/account"
curl -s -b jar -H "$O" -X PUT -H 'Content-Type: application/json' \
  -d '{"display_name":"Chris","email":"chris@example.com"}' "$H/api/plugins/console/v1/account/profile"
curl -s -b jar "$H/api/plugins/console/v1/approval-inbox/access"  # methods, session, lockout, step_up
```

Expected refusals:

| Request | Answer |
|---|---|
| no browser session | 401 `session_required` |
| write without a same-origin `Origin`, or with an `Authorization` header | 403 |
| update/reset without `If-Match` | 428 `precondition_required` |
| update/reset with a stale ETag | 412 `value_conflict` |
| value out of range (for example `stale_after_s` 5) | 422 `validation_failed` with a JSON pointer |
| write to a read-only or env-backed field | 422 / not editable |
| operator-only key (`elevation_ttl`) without an operator-token session | 403 `operator_access_required` plus `step_up{action,risk,summary,ref}` and `methods` |
| `POST /access/challenge` or `/access/verify` | 501 `unsupported` until a Stepper is wired |
| display name empty or over 80, bad email | 422 with pointer |

## Security notes

- Settings that change security behaviour (`elevation_ttl`) are operator-only keys; the refusal carries the step-up hint, and the client retries with the same `Idempotency-Key`. Nothing here adds a guard or level beyond the existing requirements table.
- Credentials sent to `/access/verify` are never logged and responses are `no-store`.
- The Alerts group is app-wide (the library supports `app` scope only), not per user.
- Features register their settings as groups in the registry. A private settings route is a review finding.
- Every route is mounted by explicit pattern; the `/api/plugins/` catch-all is unguarded and must not serve settings.

## Troubleshooting

- Manifest empty: no group registered (registration failures stop start with the group id in the log).
- Value does not change after an update: check `apply_state`; `pending_restart` takes effect after the target restarts, and env/file layers outrank a default but not an override unless the group says so.
- 409 after saving elsewhere: reload the manifest; the renderer sends intent only and the host re-reads ETags.
- `session.state` stays `active` after expiry: Tangent has to keep reporting the past `elevation_expires_at` (unverified).
