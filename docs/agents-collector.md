# Agents collector (A1)

`internal/agentscollector` follows Tether's environment event stream and keeps a derived, rebuildable
read model (migration 024). Design `tesseract:01M6547NTB3FR4P49GQRP8WC4B` section 13 (A1), decision E1.
Derivation (state, one-liners, context) is A2; the bus feed is A4; the sessions API and SSE are A3.

- **On by default** when a Tether address is set; `TACHYON_AGENTS_COLLECTOR=0` turns it off. Uses the
  usual `TETHER_TOKEN_FILE`, which must be read scope only. A token over plain http to a non-loopback host
  is refused (`upstreamauth.Guard`).
- **Sources**: `GET /environment/snapshot` (strategy A), else `GET /sessions?state=running` + the newest
  event seq from `GET /events?limit=1` (strategy B, recorded in the status). Live: `GET
  /environment/events?after_seq=N`. Backfill: `GET /events?since_seq&cursor&limit` newest first, at most 20
  pages. All wire shapes are UNVERIFIED against a live Tether and are tested with recorded frames.
- **Gaps**: a stream `gap` frame, or a cursor older than 2 minutes at start, records a row in
  `agents_gaps`. History fills it when it can (`backfilled=1`); otherwise it stays open and is reported in
  `GET agents/collector` (`open_gaps`). Nothing is silently filled. An unreachable Tether shows
  `collecting: false`, never an empty healthy page.
- **Resilience**: backoff 3 s doubling to 1 min, a 60 s watchdog (no frame or keepalive reconnects), panic
  recovery that never prints the panic value, idempotent apply (`tether_event_id` unique), the cursor
  moves in the same transaction as the rows, a bounded queue (4096) and 1 s batching.
- **Privacy**: `session.output` payloads are never parsed (counted, and used as an activity time only).
  Other payloads are read for an allowlist of identifier fields; stored summaries are built from the kind
  and those fields, never from payload text. A test plants a secret in output, tool args and message
  bodies and greps every table.
- **Retention**: events 7 days, gaps and ended sessions 30 days, pruned in batches of 1000 every 10 minutes
  with a receipt in `agents_collector_state`. Samples (downsampling) arrive with A2.
- **Not here yet**: `dropped_output_frames` stays 0 until A2 parses output in memory and can shed it under
  load; strategy B does not call per-session health; bus topics report `off`/`no_emitter` until A4.
