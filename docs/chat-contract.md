# Chat backend contract (T0)

Source design: `tesseract:01M651VJ6DT88Q3YAJS9KJH6QJ`. The machine-readable contract is
`internal/chatapi/openapi.yaml`; fixtures are in `internal/chatapi/fixtures/`.

## Using the stubs

Run Tachyon with `TACHYON_CHAT_STUBS=1`. Every chat route (`/api/plugins/console/v1/chat/...`) and terminal route
(`/api/plugins/console/v1/terminal/...`) answers with a fixture in the final shape. Writes and terminal/workspace-content
reads still require an operator session; the stub changes data, not guards. Send `X-Stub-Status: 409` (any 400-599) to
get the error shape. `GET /chat/openapi.yaml` serves the contract. The terminal websocket answers 501 (frames are in the
spec). With the flag off nothing is mounted. Stub responses carry `X-Tachyon-Chat-Stub: 1`.

## thread_ref

`app:<app_agent_threads.id>` or `tether:<session_id>` (chatbridge grouping key: session_id, else local_session_id).
Tachyon-owned and stable; never the runtime session id. Parse with `chatapi.ParseRef`. It travels in path segments as is.

## Event envelope

Flat JSON per SSE `data:` line: `{"type", "thread_ref", "at", ...payload}`. `at` is unix ms. Types: message, delta, tool,
end, delivery (existing, unchanged); card, plan, activity, panel, topic, bookmark, workspace, context, thread,
terminal.run (new). The activity record's own type is `activity_type` inside an `activity` event.

Activity-class events (activity, plan, card, topic, bookmark, workspace, terminal.run) carry `id: <seq>` and are replayed
after `Last-Event-ID`; the rest are live signals, so after a reconnect refetch state. 15 s keepalive comments. A slow
subscriber is dropped and must refetch. Events are signals, not state.

## SSE replay

`scripts/chat-sse-replay -1` prints the stub stream and exits; `-l 3` resumes after id 3; `-r -` is the global spine.

## Conventions

snake_case JSON, unix-ms timestamps, errors `{code,message}` (400 invalid, 403 origin_denied|operator_session_required,
404 not_found, 409 conflict, 429 rate_limited, 503 not_configured), revision-checked writes answer 409 on a stale revision.
