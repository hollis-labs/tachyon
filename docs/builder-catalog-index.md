# Builder catalog index (B1)

The Builder's read model of the Tether catalog (design `design_tachyon_builder_registry_backend_20261030`
section 3.1-3.2). Tether stays the system of record; the index is rebuildable. Package `internal/catalogindex`,
migration `030_catalog_index.sql`.

## What it does

- A poller (30 s) lists the catalog files, reads those whose listing fingerprint moved (or, when the listing has
  none, whenever the newest plan id moved or a 5-minute full scan is due), hashes the content (`sha256`, the
  revision of an entity), parses it into typed rows and keeps a masked snapshot of every change.
- Only `agents/`, `launches/`, `projects/` and `skills/` are read. `mcp-servers/`, `providers/`, `sandbox-profiles/`
  and `global.yaml` are never read (credentials); `mcp-servers/` is counted by presence for the tool-server badge.
- Snapshots are written when the Builder applies a plan (`source: builder`, with plan id and actor, via
  `Index.NoteApplied`, called by the change service in B3) and when a sha change is observed (`source: observed`).
  Retention: newest 50 per path and 90 days.
- Events: `catalog {observed_at, changed_paths[], source}` (replayable) and `summary` (live snapshot) on
  `GET /api/plugins/console/v1/builder/stream`; `GET .../builder/summary` is the tab-count call. The first pass
  after a fresh install publishes no catalog event.

## Secrets

Environment values (`env`, `overrides.env`, `provider_overrides`), values under secret-named keys
(key/secret/token/password/credential/auth) and secret-shaped strings are replaced by `***` before anything is
stored; key names and a secret-like count are kept. YAML is edited as a node tree, so comments, key order and
unknown keys survive. Content that does not parse is skipped, never stored raw. Nothing is logged but paths.

## Settings

`TACHYON_CATALOG_INDEX=0` turns it off. It needs `TETHER_ADDR` and the read-scope `TETHER_TOKEN_FILE` (shared with the
Agents collector); it refuses a credential over plain http to a non-loopback host. With `TACHYON_BUILDER_STUBS=1`
the stub keeps the whole prefix and the real summary/stream are not mounted.

## Known gaps

- The Tether wire shapes (`GET /catalog/files`, `?path=`, `GET /catalog/history?limit=1`) are UNVERIFIED against a
  live Tether; parsing is tolerant (bare array or wrapped under files/items/entries; content or body).
- `launches.live` is null until B2 (`used_by`); `open_changes`, prompts and policies are 0 until B3/B5.
- Index rows are not yet served as agents/launches lists; that is B2.
