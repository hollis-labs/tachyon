# Session Ops

Stateless session operations through go-tether-client v0.7.0. Tether owns all
session state and execution. The plugin uses `pluginkit.Dispatch` to carry
capabilities and verbs over plugin-sdk's `command/execute` protocol.

Set `TETHER_ADDR` to a go-tether-client listen address (`unix:/path`,
`tcp:host:port`, or `http(s)://host`). Host configuration `tether_addr` takes
precedence. With neither set, the client uses its default Tether Unix socket.
Initialization constructs the client without requiring a reachable daemon.

Invoke verbs with `POST /api/verb/<verb>` and a JSON body:

| Verb | Payload | Result |
| --- | --- | --- |
| `session_create` | `launch_id`, optional `boot_prompt`, `idempotency_key` | Session allocated by Tether; launch execution belongs to launch-ops |
| `session_read` | `id` | Session details |
| `session_list` | Optional `agent_id`, `status`, `provider`, `limit`, `cursor` | `sessions`, optional `next_cursor` |
| `session_attach` | `id` | Validated running session metadata for a server-side Tether consumer |
| `session_stop` | `id` | Stop acknowledgement |
| `session_submit` | `id`, `text` | Turn submission acknowledgement |
| `session_history` | `id`, optional `limit`, `cursor`, `since_seq` | Provider event log, including turn events, and optional `next_cursor` |

`agent_id` maps to Tether's logical agent ID. `provider` matches its provider ID.
Agent and provider filters apply within each Tether page. Always follow
`next_cursor`, even when a filtered page is empty. Status filtering is sent to
Tether as `state`. Event payloads remain the provider's `payload_json`; Tachyon
does not reconstruct or store conversation history.

Attach returns metadata (`session_id`, `provider_id`, `state`, `transport`,
`streaming`), without opening a stream or returning a browser-facing provider
URL. A server-side consumer uses Tether's `AttachSession` separately. This
keeps long-lived streams off the plugin's serial command pipe.

Pause/resume are absent from both the implementation and advertised verbs:
the released Tether client has no session pause/resume API. Resuming a logical
agent is a different operation. Any follow-up belongs in Tether first.

The declaration includes the Sessions navigation group and its list/history
items. Rendering these declarations and implementing the React screens are
separate host/frontend work; this plugin adds no browser routes.

Turn submissions have a 30-second context deadline (a shorter caller deadline
wins), so a stalled Tether turn releases the serial plugin pipe. Timeout returns
`timeout` with unknown completion: a submitted turn may still run upstream, so
inspect session history before retrying. Other operations use the client's
five-second HTTP timeout; create performs allocation and a follow-up read.
