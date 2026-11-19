# Console item state (/console/state)

The one shared store behind the UI's `StateStore` (PR2, CW-20261030-0087). Pull-request triage uses it now;
Alerts compat (AL1) sits on it (no second implementation).
Package `internal/itemstate`, migration `031_console_item_state.sql`. Contract: `internal/forgeapi/openapi.yaml`.

- `GET /api/plugins/console/v1/console/state?prefix=` lists rows (newest first, max 1000).
- `PUT .../console/state/{key}` applies one op; slashes in a key are `%2F`. Body `{type, note?, until?|preset?, resolution?, expected_version?}`,
  `type` is read, dismiss, snooze, settle or reopen. Snooze takes a preset (1h, 4h, tomorrow) or a future `until` within 90 days: epoch milliseconds (what the client's `HttpStateStore` sends) or an RFC 3339 string.
- `POST .../console/state/bulk` `{ops:[{key, ...}]}` (1-100) applies each independently; per-key `{ok, state|error}`.
- Row (the client's `ItemState`, times in epoch ms): `{key, status unread|read|dismissed|snoozed|settled, snoozeUntil?, wokeAt?, resolution?, note?, by, updatedAt, version}`.
  `expected_version` is optimistic: 0 means create-only-if-absent; a mismatch is 409 `conflict` with the current `version`.
- An expired snooze wakes to `unread` (wokeAt set) within 30 s; `Store.OnWake` reports the woken keys for event producers.
- Keys are any string up to 200 characters without control characters: the console client keys alert rows by event id and PR triage uses `pr-inbox:`/`pr-assign:`. `Store.Prefixes` can restrict this if ever needed. Rows hold no content.
- Reads are open like every console read; PUT and bulk need an operator session and same origin, and refuse an `Authorization`
  header (agents). `by` is `operator` until sessions carry a name. With `TACHYON_FORGE_STUBS=1` the stub owns the prefix.
