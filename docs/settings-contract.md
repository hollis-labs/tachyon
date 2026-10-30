# Settings, Account and Assurance contract (ST0)

Design: `design_tachyon_settings_account_assurance_backend_20261030` (Tesseract 01M65GDQC0KC87CMT6CJZTFFD2).
Code: `internal/settingsapi` (contract types, group declarations, in-memory stub, `openapi.yaml`). Dev flag:
`TACHYON_SETTINGS_STUBS=1`, mounted by `internal/server/register_settings.go` one explicit pattern at a time.

## Three parts

1. **Settings (admin manifest).** The stub is the libs `adminhttp` handler (ui-go v0.4.0, already in `go.mod`) over an
   in-memory layered `GroupBackend`, so the manifest, snapshot, validate, update and reset shapes, ETag handling and
   the 409 `manifest_changed`, 412 `value_conflict`, 422 `validation_failed` and 428 `precondition_required` errors
   are the library's own, not a copy. Routes under `/api/plugins/console/v1/admin`:
   `GET manifest`, and per group `GET settings/{id}`, `POST settings/{id}/validate|update|reset`.
   Update and reset need `If-Match` with the ETag of the last read. The renderer sends intent only
   (`{revision, set, unset}` or `{revision, keys}`); the host owns ETags and conflict handling.
2. **Account.** `GET /account` (whoami) and `PUT /account/profile`.
3. **Assurance extension.** `/access` gains `methods`, `session`, `lockout`, `step_up`; a refused write carries a
   `step_up` hint; `/access/challenge` and `/access/verify` are specified and scripted in the stub.

## Verified facts

- adminhttp's path layout is `<BasePath>/admin/manifest` and `<BasePath>/admin/settings/{id}[/validate|update|reset]`
  (read from `adminhttp/handler.go`, `matchesMount`). With `BasePath = /api/plugins/console/v1` it matches the paths
  `plugins/console/ui/src/prefs/manifest.ts` already assumes (`/admin/settings/<group>/...`).
  `TestManifestIsLibraryValidAndMatchesAdminLayout` decodes the stub manifest into the library's `admin.Manifest`
  and compares every declared endpoint with `AdminRoutes`.
- Field types are flat (string, boolean, integer, number): list-like values are comma or multi-line strings.
- Scope is `app` only (the library supports nothing else). The Alerts group the UI declares as `scope: user` is
  served app-wide: it applies to everyone using this Tachyon.

## Groups declared in the stub

`general` (cache policy fields, `elevation_ttl`), `chat`, `agents`, `transcripts` (read-only, env), `alerts`.
Together they cover every source kind (`override`, `env`, `file`, `default`) and both read-only and `pending_restart`
apply states. `elevation_ttl` needs operator access and is `pending_restart` with target `tangent`. Later designs
(fileview, forge, approvals batch ops) must register their settings as groups here (ST1 registry) rather than
adding private routes; keep the setting behind a small interface in the owning package until then.

## Step-up refusal

A write that needs more assurance than the session has is refused with 403 `operator_access_required` (the code the
assurance package already uses; the design called it `assurance_insufficient`) plus what is being confirmed:

```json
{"code":"operator_access_required","message":"…","step_up":{"action":"settings.write","risk":"medium","summary":"…","ref":"general.elevation_ttl"},"methods":[{"id":"operator_token","available":true},…]}
```

On admin routes the same object sits under `error` (the library's envelope). The client retries with the same
`Idempotency-Key`; drafts are never cleared by the dialog.

## Access contract (stub path `/settings-stub/access`)

The real route is the existing approval-inbox `GET /access`; ST4 extends it additively. The stub uses its own prefix so
both can run side by side. `Access` embeds `assurance.Access`, so ST4 cannot drift from what approvals serve.

- `methods[{id: operator_token|password|passkey, available}]` (only `operator_token` is available today)
- `session{state: active|expired|none, expires_at?}`, `lockout{locked, retry_after_s?, attempts_left?}`, `step_up{supported}`
- `POST /access/challenge {action, ref?, digest?}` returns `{challenge_id, methods, expires_at, display{action,risk,summary?}}`.
  Plain sign-in is a challenge with action `elevate`.
- `POST /access/verify {challenge_id, method, credential}` returns `{ok, assurance, grant{scope, expires_at, remember_s?}}` or
  `invalid_credentials` (403, no `attempts_left` yet), `locked` (429, `retry_after_s`), `challenge_expired` (410),
  `method_unavailable` (400). Credentials are never logged; responses are `no-store`.

Stub script: credential `stub-operator-token` signs in (30 min); anything else fails; the fifth failure locks for a
minute; challenge id `ch_expired` and unknown ids read as expired; `password` and `passkey` read as unavailable.
`X-Stub-Scenario` on `/access` and `/account`: `expired`, `locked`, `elevated`, `no_step_up`, and `unknown`,
`identified`, `verified` (account). `X-Stub-Status` forces 401, 403, 500 or 503. `POST /access/reset` clears the stub.

## Account

`GET /account` returns `{identity{state: identified|unknown, display_name?, email?, source: local-profile|session,
assurance: local|verified, verified_by?}, access, profile_updated_at?, can_set_profile}`. `verified` means the operator
token is in use on this session, never that the profile name was checked. `PUT /account/profile {display_name 1-80,
email? syntactic, 254}` is a same-origin session write; errors use the admin error shape with JSON pointers.
The profile is one local profile for the operator (decision S1), stored later in `app_settings` as `account.profile`.
It is never an authorization input.

## Stub behaviour and limits

- Nothing is durable. Writes need a same-origin browser request and `Authorization` headers are refused. The mount
  does not use `RequireOperator` (same precedent as the Alerts stub: it holds no real data, and a wrapper would hide
  the step-up refusal the stub exists to show).
- The operator flag is process-wide in the stub (single operator); the real implementation resolves it per request.
- Unverified against a live Tangent: lockout `retry_after_s` is an estimate (Tangent sends no `Retry-After`, request R1).
- Not in this slice: the real registry and `app_settings` backend (ST1, review-gated), group registration (ST2),
  the account store (ST3), the real `/access` fields and `Provider.Challenge/Verify` (ST4).
