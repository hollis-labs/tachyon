# Transcripts backend contract

Contracts and fixture-backed stubs for the Transcripts GUI (TR0, CW-20261030-0097).
Design: tesseract key `design_tachyon_transcripts_backend_20261030`. Nothing here talks to
Fragments Engine; the real Stage 1 bridge work is TR1 (review-gated: guard + content search).

- Package: `internal/transcriptsapi` (`contract.go`, `stub.go`, `openapi.yaml`, `fixtures/`).
- Enable: `TACHYON_TRANSCRIPTS_STUBS=1`. With it on, the stub replaces the transcripts bridge
  under `/api/plugins/console/v1/transcripts` (GET-only, no cache, no upstream). Off by default.
- Spec: `internal/transcriptsapi/openapi.yaml`, also served at `GET .../transcripts/openapi.yaml`.

## Routes

| Route | Stage | Notes |
|---|---|---|
| `GET /snapshot` | 1 | adds `snapshot_meta` {source_state live/cached/stale/unavailable, observed_at, fetched_at, complete, limit, total_available, omitted_count, error?}. Unavailable returns the saved snapshot with 200 plus the error |
| `GET /sessions/{id}` | 1 | `turn_limit` 1..200 (default 50), `turn_offset`; any valid 64-hex id (deep link), unknown 404 `not_found`; `turn_from_seq` is Stage 2 |
| `GET /search` | 1 | `q` (<=256), `mode` text/summary/semantic/hybrid, filters, `limit` <=50, opaque `cursor`; never cached; `notice` must be shown |
| `GET /sessions/{id}/find`, `/errors` | 2 | need FE per-turn FTS (F2) |
| `GET /stats` | 2 | optional; needs FE F4 |
| `GET /status` | 1/2 | modes and semantic availability; `ingest` is Stage 2 (F3) |

Errors are `{"code","message"}`. Unknown query parameters are 400 `invalid_args`. Writes are 405.
An `Authorization` header is refused (403 `forbidden`): browser session only; the owner-only FE
design means no agent surface. The real guard is the assurance requirement `transcripts.read`
(browser-session, reserved operator mapping for search/find and tool target/excerpt fields).

## Session fields (live shape)

The live bridge and the Transcripts page use these names; the contract follows them. A session summary is
`{id, tool, kind, title, project, model, started_at, ended_at, turns, tokens, tool_calls, errors, tags, enriched}`:
`tool` is the product (`claude`, `codex`, `chatgpt`; `facets.tools` lists them), `kind` is `main` or `subagent`,
`started_at` and `ended_at` are ISO 8601 strings (absent or null when undated), the counts are `turns`, `tokens`,
`tool_calls` and `errors`, and `enriched` replaces the old `has_enrichment`. The earlier TR0 names (`turn_count`,
`tool_call_count`, `error_count`, unix-ms times, `kind` claude|codex|chatgpt, `tools[]`) are gone. Turn and tool-call
entries inside `GET /sessions/{id}` still carry their TR0 shapes (`started_at` in unix ms); they have no live
counterpart yet. `GET /status` adds `capabilities {search, find, errors, stats}`; a client without it probes and
treats a 404 as absent. The stub reports all four true.

## What the data can and cannot say

- Tool cards: Stage 1 has name, status (ok/error/none), result size and times. `target` and
  `error_excerpt` appear only after FE F1 and a re-ingest; the UI renders nothing until present.
- Search is session-level in Stage 1. Semantic matches summaries, not passages (max 100, no paging).
  Highlights are `brackets` (best-effort) until FE F2 supplies `ranges`.
- Codex errors are heuristic (`codex_errors_are_heuristic`); ChatGPT exports carry no tool calls (`no_tool_data`).
- All transcript text, targets and excerpts are untrusted plain text; redaction is regex-based.

## Stub controls

`X-Stub-Status: N` forces an error. `X-Stub-Scenario`: on `/snapshot` `cached|stale|unavailable|partial|empty`;
on `/search` `semantic_unavailable|empty|ranges`; on `/status` `degraded`.

Fixture sessions (constants in `contract.go`): Claude (60 turns, targets, two error excerpts),
Codex (heuristic error), ChatGPT export (no tools), an undated session, and a valid unknown id.
