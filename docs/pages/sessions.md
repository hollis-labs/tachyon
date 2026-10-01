# Provider Sessions pages

Open **Active Sessions** or **Session History** in the shell's **Sessions**
group. The registered routes are `/sessions` and `/sessions/history`; these
are shell navigation identifiers, not standalone browser deep links. The UI
is served under `/sysop/`.

These pages inspect and act on Tether-backed provider sessions. The separate
**Durable Sessions** page is a Nanite surface, not this session lifecycle.

## Capabilities

Navigation requires a registered page and its declared verb. Each additional
read or action is gated independently; discovering a verb does not authorize
an operator or guarantee provider availability.

| Surface | Required verb | Declared effect |
| --- | --- | --- |
| Active Sessions page and list | `session_list` | `reads` |
| Selected session details | `session_read` | `reads` |
| Connection metadata inspection | `session_attach` | `reads` |
| Session History page and detail history | `session_history` | `reads` |
| Create session form | `session_create` | `open_world` |
| Stop session form | `session_stop` | `open_world` |
| Send turn form | `session_submit` | `open_world` |

## Lists, details and history

The list offers **Agent ID**, **Provider ID** and **Session state** filters.
Requests use a limit of 50. State is passed to the provider; agent and provider
filters apply to each returned provider page. An empty filtered page can still
have more results. **Load more sessions** passes the provider's opaque string
cursor unchanged; it is not a numeric offset. Refresh starts from the first
page. Appended rows are combined by session ID.

Selecting a session shows its IDs, provider, state and timestamps, when the
corresponding read is available. Missing optional fields and invalid dates
show **Not available**. **Inspect connection metadata** displays session and
provider IDs, state, transport and the provider's streaming flag. It opens no
stream and supplies no browser-facing provider URL, even when streaming is
reported as supported.

History can be opened from session details or by entering a **Session ID**
and nonnegative integer **Since sequence** on the independent history page.
History requests also use a limit of 50, but their continuation cursor is
numeric. Events show sequence, kind, scope, timestamp and literal payload text;
appended events are combined by sequence. Tachyon does not reconstruct or
store a conversation from these events.

Reads begin when a page or selection loads; further refresh is manual. There
is no live stream or automatic session/history polling. Loading, empty,
unavailable-capability and provider-error states are distinct. A continuation
failure keeps previous rows or events visible. A list cursor that repeats the
current cursor, or a history cursor that fails to advance, reports an error.
Replies belonging to an earlier selection cannot replace the current read.

## Explicit provider actions

**Create session** requires an existing provider launch ID, with optional boot
prompt and operator-supplied idempotency key. It allocates through the provider;
it does not prepare or execute a Launches-page launch intent. The key is passed
through and retained in the form. The Launches page uses a local launch-intent
ID in its key, so this form cannot infer that key from a provider launch ID.
The confirmation warns that a launch which already has a session may receive
another one. Tachyon makes no cross-page or provider deduplication guarantee.

Create, stop and send-turn actions require explicit confirmation. Opening the
confirmation or cancelling sends no mutation request. Confirming sends one
request per attempt; a synchronous in-flight guard prevents double activation
from sending another while that request is pending. This is a local UI guard,
not an exactly-once execution guarantee. Forms retain their draft values.

A successful create response identifies the returned session. A stop response
acknowledges the request; refresh details to inspect its current state. A turn
submission acknowledgement does not mean that the turn finished. The adapter
bounds turn submission with a 30-second context ceiling; an earlier deadline
or HTTP timeout can end the wait sooner. A timeout does not prove execution
stopped. Transport failures and incomplete acknowledgements are shown as
**Completion unknown**: inspect details and history before starting another
action. Writes are never automatically retried.

## Operator decisions and limits

An `ask` envelope shows the shared [operator-decision panel](hitl-ui.md),
including read-only host decision tracking where correlation is available.
The affected mutation form retains its draft and remains locked while the ask
notice is present. Approval does not replay the request. **Start a new action**
clears the local notice only; another explicit confirmation is required before
any new mutation. Continuation, resume and automatic retry are unavailable.
Read asks also use the decision panel without replaying the original read.

These pages do not provide pause/resume controls, provider streaming,
conversation persistence or automatic lifecycle orchestration. Provider state
and history remain upstream; a displayed acknowledgement is not proof of
completed execution.

Source: [list and detail page](../../frontend/src/pages/provider-sessions.tsx),
[history page](../../frontend/src/pages/session-history.tsx),
[mutation form](../../frontend/src/components/sessions/mutation-form.tsx),
[Sessions API](../../frontend/src/api/sessions.ts),
[plugin declarations](../../plugins/session-ops/capabilities.json), and
[provider adapter](../../plugins/session-ops/tether_adapter.go).
