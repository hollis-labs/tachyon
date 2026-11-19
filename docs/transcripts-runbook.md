# Transcripts: running it, curl matrix, security notes (TR7)

Shapes are in `docs/transcripts-contract.md` and `internal/transcriptsapi/openapi.yaml`. This page is the operator's view.
Design: `tesseract:01M65ESWBCDAC4QZPY40HNH9HM`. Tachyon is a read-only bridge; Fragments Engine (FE) owns the transcripts and their redaction.

## What runs

| Part | Where | Switch |
|---|---|---|
| Bridge: `/snapshot`, `/search`, `/status`, `/stats`, `/sessions/{id}`, `/find`, `/errors` | `internal/transcripts` | needs FE's URL and private token file on the Tachyon host; without them every read is 503 `configuration` (`/snapshot` serves the saved copy) |
| Snapshot size | `/snapshot` pages FE `/sessions` | `TACHYON_TRANSCRIPT_MAX_SESSIONS` (default 5000) |
| Fixture stub instead of the bridge | `internal/transcriptsapi` | `TACHYON_TRANSCRIPTS_STUBS=1` (no upstream, for UI work) |

Which optional reads work comes from `GET /status` `capabilities` {search, find, errors, stats}, taken from the engine's own `features` list.
On an older engine `/find`, `/errors` and `/stats` answer 501 `unsupported`.

## Behaviour to know

- Everything is GET. Other methods are 405 `read_only`. Unknown paths are 404 `not_found`; unknown query parameters are 400 `invalid_args`.
- `/snapshot`: concurrent requests share one build. A good build is saved (`transcript_snapshot`, metadata only: titles, projects, counts; no turn text).
  If FE is unreadable (denied, unconfigured, unreachable, remote error) the saved copy comes back 200 with `snapshot_meta.source_state = "unavailable"` and `snapshot_meta.error`.
  `complete=false` means the snapshot hit the session cap; `omitted_count` includes undated sessions the time cutoff drops.
- `/sessions/{id}`: `turn_limit` 1..200 (default 50), `turn_offset` or `turn_from_seq` (not both). Any valid 64-hex id is readable, so a deep link works for a session outside the snapshot; unknown id is 404.
- `/search`: `q` 1..256, `mode` text|summary|semantic|hybrid (refused if FE's `/status` does not list it), `limit` 1..50, opaque `cursor`. Semantic matches summaries, returns at most 100, no paging; show the `notice`.
- Search, find and errors are never cached and the query text is not logged. Responses carry `Cache-Control: no-store`.
- Errors are tool-call result errors. Codex errors are heuristic; ChatGPT exports have no tool calls.

## Curl matrix

Browser session only. Use the browser's cookie jar (`-b`) in practice.

```sh
B=https://tachyon.example/api/plugins/console/v1/transcripts
S=$(printf 'a%.0s' $(seq 64))                                    # a 64-hex session id

curl -s -b jar "$B/status"                                       # modes, capabilities, ingest
curl -s -b jar "$B/snapshot"                                     # list + facets + snapshot_meta
curl -s -b jar "$B/sessions/$S?turn_limit=50&turn_offset=0"      # one session, first page
curl -s -b jar "$B/sessions/$S?turn_from_seq=120"                # window starting at a found turn
curl -s -b jar "$B/search?q=migration&mode=text&limit=20"        # content search
curl -s -b jar "$B/search?q=deploy&mode=hybrid&tool=claude&has_error=1"
curl -s -b jar "$B/sessions/$S/find?q=timeout&after_offset=0&limit=50"
curl -s -b jar "$B/sessions/$S/errors"                           # for "Next error"
curl -s -b jar "$B/stats?since=2026-10-01T00:00:00Z&tool=codex"  # whole-database totals
```

Expected refusals:

| Request | Answer |
|---|---|
| no browser session | 401 |
| any call with an `Authorization` header | 403 `forbidden` (owner-only data, no agent surface) |
| POST, PUT, DELETE | 405 `read_only` |
| unknown path or unknown session id | 404 `not_found` |
| unknown query parameter, `q` empty or over 256, `turn_limit` outside 1..200, both `turn_offset` and `turn_from_seq` | 400 `invalid_args` |
| `/find`, `/errors`, `/stats` on an engine without the feature | 501 `unsupported` |
| FE URL or token missing | 503 `configuration` (`/snapshot` serves the saved copy) |
| FE denies the credential | 401/403 `authentication` (`/snapshot` serves the saved copy) |
| FE rate limits | 429 `rate_limited` |
| FE down | 502 `unreachable` |

## Security notes

- The guard is the assurance requirement `transcripts.read` (browser session; `Requirements` table). It sits outside the API cache, so a refusal is never cached or served from it.
  A reserved operator mapping for search, find and tool target/excerpt fields is for the hardening phase; today the posture is browser session.
- Transcripts are the most sensitive data Tachyon shows. The bridge forwards only named routes and validated parameters; it never passes an arbitrary path to FE.
- Tachyon stores no transcript text. The only copy is the last-saved snapshot, metadata only, in localstore. No read-audit table exists yet.
- Redaction is FE's and is regex based: it removes known token shapes, not every secret. Transcript text, tool targets and error excerpts are untrusted plain text: never render as HTML, never follow links in them.
- Tachyon's FE credential is currently FE's owner scope. Narrowing it (TR6) is an FE request, not done here.
- Every route is mounted by explicit pattern; the `/api/plugins/` catch-all is unguarded and must not serve transcripts.

## Troubleshooting

- Page shows "source unavailable" with old data: `snapshot_meta.error` says why; fix FE's URL/token on the host.
- Snapshot says partial: raise `TACHYON_TRANSCRIPT_MAX_SESSIONS` or narrow with search filters.
- Find or Next error disabled: `GET /status` `capabilities` is false; the engine needs the per-turn search change.
- No tool target or error excerpt on old sessions: they appear only after FE stores them and the session is re-ingested.
- "Updated Nm ago" is fetch time unless `status.ingest.last_ingest_at` is present.
