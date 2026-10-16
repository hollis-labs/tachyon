# History Ops

Read-only, stateless access to chat transcript history in Fragments Engine (FE):
sessions, search, facets and projects. FE owns and indexes the data (redacted
text and metadata only; no thinking text, raw archives or attachment bytes) and
runs text, summary, semantic and hybrid search. This is historical, not
realtime. Use it for history views, reporting and leaderboards.

Configure `fragments_url` (or `FRAGMENTS_URL`) and `fragments_token_file` (or
`FRAGMENTS_TOKEN_FILE`; a 0600 file holding FE's owner bearer token).

| Where Tachyon runs | `fragments_url` |
| --- | --- |
| On agent-os (same host as FE) | `http://127.0.0.1:8091` (the default). The transcript routes require a loopback peer and Host, and Caddy's owner-device list excludes agent-os itself |
| On an owner device (home-laptop, work-laptop, iPhone) | `https://fragments.nanite.cloud` (Caddy admits only those tailnet addresses, and rewrites Host) |

The plugin refuses a token over plain `http` to a non-loopback host and never
follows redirects. FE's transcript feature is off by default (`ui.transcripts.enabled`);
when off every route answers 404, which this plugin reports as `not_found`.

| Verb | Payload | FE route |
| --- | --- | --- |
| `history_status` | none | `GET /v1/transcripts/status`: `enabled`, `available`, `semantic`, `modes`, `auth` |
| `history_sessions` | optional `q`, `mode` (`text`\|`summary`\|`semantic`\|`hybrid`, needs `q`), `tool`, `project`, `model`, `kind` (`main`\|`subagent`), `tag`, `since`, `until` (RFC 3339 or `YYYY-MM-DD`), `has_error`, `has_enrichment`, `sort` (`started`\|`turns`\|`tokens`\|`tool_calls`\|`errors`), `order`, `limit` (1-200), `cursor` | `GET /v1/transcripts/sessions` |
| `history_session` | `id` (64 hex), optional `turn_offset`, `turn_limit` (1-200) | `GET /v1/transcripts/sessions/{id}` |
| `history_facets` | none | `GET /v1/transcripts/facets` |
| `history_projects` | optional `q`, `limit` | `GET /v1/transcripts/projects` |

Responses are FE's JSON, unchanged. Errors carry a fixed local code
(`unauthenticated`, `forbidden`, `not_found`, `unavailable`, `timeout`,
`response_too_large`, `provider_error`) and the HTTP status; FE's text is never
passed through. FE has no aggregate endpoint: leaderboards and reports are built
from session rows (`tokens`, `tool_calls`, `errors`, `turns`, `model`, `tool`,
`project`, `started_at`) paged with `cursor` and filtered by date.
