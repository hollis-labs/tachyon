# Alerts backend contract (AL0)

Final shapes for the Alerts backend: `internal/alertsapi/openapi.yaml`, fixtures in
`internal/alertsapi/fixtures/`, rule-pack data in `internal/alertsapi/rulepack/`. Design
`tesseract:01M65CRJAF23AP7YBEBGJNE14M`, UI limits `tesseract:01M65CS9P9WPADX59WY4FCH9YK`.
Run the stub with `TACHYON_ALERTS_STUBS=1`; replay the stream with `scripts/alerts-sse-replay [-l SEQ] [-1] [-g]`.

- Prefix `/api/plugins/console/v1/alerts`. Compatibility routes `GET /console/state` and
  `PUT /console/state/{key}` (the existing StateStore client; `key` is an `alert_id`) are served too and go
  away after AL8. They are last-writer-wins (no `expected_version`).
- Reads are open to a browser session. Writes need a same-origin request (`Origin`); no operator
  elevation (design section 6). An `Authorization` header is refused. Routes are mounted one by one:
  the `/api/plugins/` catch-all is unguarded.
- Errors are `{code,message}`. `X-Stub-Status: 400|403|404|409|429|503` forces an error from any stub route
  except the stream. `X-Stub-Scenario: degraded` on `/sources`; `gap` on `/stream`.
- Alerts are server objects with stable ids (`al_...`), one shared operator state, and a `version` for
  `expected_version`; a stale write answers 409 `version_conflict` with `current`.
- **Ops**: read, unread, dismiss, snooze (`preset` 15m|1h|4h|tomorrow or `until_ms`), defer (`until_ms`
  and/or `until_ref_change{kind,ref}`), settle (`resolution`, `note`), reopen ("Restore"). Bulk: read or
  dismiss, `ids` (<=200) or `scope: all_active`, optional `expected_seq`.
- **No decision path here.** Approve and Deny go through the Approvals endpoints. An approval alert carries
  an `approval` block on `GET /{id}` (`item_id`, `expires_at_ms`, `class`, `risk_display`,
  `provider_handoff`); the drawer embeds the Approvals component by `item_id`. With `provider_handoff` the
  drawer shows the neutral read-only card "Approved through the provider". The alert settles itself from
  the approvals event.
- **Toasting is the server's decision**: every `alert.*` frame has `toast`. Critical always toasts, an
  escalation or a snooze wake re-toasts, a coalesced repeat does not. The client keeps only its local Quiet
  toggle. Use `server_now_ms` for countdowns.
- **Prefs**: `notify_level` blockers|warnings|everything (error+, warning+, debug+), `toasts`, `quiet`,
  `quiet_hours{enabled,start,end,tz}` (one IANA zone), `muted_sources`, `coalesce_window_sec`.
  `POST /prefs/migrate` imports the old localStorage value once (`toastMinSeverity` picks the level; the
  bell threshold is dropped) and returns `applied: false` afterwards. `alertsapi.MigratePrefs` is the
  mapping, reusable by AL6.
- **Stream**: flat JSON frames `{type, at_ms, ...}`; `id:` is the alert `seq`. Types: `alert.created`,
  `alert.updated`, `alert.state`, `alert.woke`, `alert.escalated`, `alert.cleared`, `alert.removed`,
  `counts`, `prefs`, `resync`. Frames that belong to one change share its id. A cursor older than the ring
  gets one `resync`: refetch `GET /alerts`. Heartbeat comment every 20 s.
- **Detail**: timeline is the last 7 days of events sharing a subject or correlation id, max 50; with none,
  one entry "This alert". `technical.fields` is allow-listed and redacted. History is 30 days.
- `GET /sources` reports ingest health (`ok|degraded|down`); show "Alerts are not updating" when a source is
  down and the Torque degraded note while Torque has no stable event ids.

## Rule pack and conformance

`rulepack/default-pack.json` is the TS default pack carried over as data (same schema as
`rules/types.ts`, plus `alert.needsYou`). `rulepack/conformance.json` lists console-envelope events and the
alert each must produce (`rule_id`, `severity`, `icon`, `title`, `summary`, `coalesce_key`, `needs_you`,
`escalate`). Both the Go evaluator (AL2) and the TS evaluator must pass every case until the TS one is
deleted (AL8); cases with `"engines": ["go"]` cover the server caps and control-character stripping that
differ (title 120 / summary 300 on the server, 140 in TS). The Go adapter maps `subjects[].type` to `kind`
and `.id` to `ref`. `needs_you` comes from the rule only, and the catch-all never sets it.

## Stub limits

State is in memory and resets on restart; times are shifted so timers look live. The stream replays a fixed
script (`fixtures/stream.json`); it does not react to ops. Kinds with no emitter yet (host reboot,
health-check failure, workstream created) are not in the fixtures.

## Prefs semantics (AL6)

`internal/alertsprefs` (pure functions): `MinSeverity` (blockers = error, warnings = warning, everything =
debug), `InQuietHours` (read in the saved IANA zone, wraps midnight, start == end is empty, an unknown zone
fails open), `Muted` (case-insensitive substring), `Validate` (adds a real zone lookup to `ValidPrefs`),
`Normalize`, `MigrateOnce`, `BellCounts` and `ToastFor`. `ToastFor` rules: critical always toasts (passes the
master switch, Quiet, quiet hours, muting and the notify level); otherwise the master switch must be on,
Quiet and quiet hours off, the source not muted and the severity at or above the level; a coalesced repeat
and state changes (read, dismiss, settle) never toast; created, raised, woke and escalated do. Everything is
stored; the flag only drives the toast, and the bell counts what the level lets through.

## Console state compatibility (`/console/state`)

The stub's `PUT /console/state/{key}` matches the console client's `HttpStateStore`: body `{type, until?, note?, resolution?}`
for any key (the client also stores approval and bus event ids here), response `ItemState` with epoch-ms numbers
(`snoozeUntil`, `updatedAt`). `until` is epoch ms or an RFC 3339 string. A key that is an alert id changes that alert;
any other key is kept as its own state and listed by `GET /console/state[?prefix=]`. The real route is the PR2 store
(#202); this is only the stub's mirror of it.
