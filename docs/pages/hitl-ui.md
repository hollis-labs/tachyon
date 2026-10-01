# Operator-decision panels

When a supported page receives a verb envelope with `status: "ask"`, it keeps
the request as an **Operator decision** panel instead of presenting approval
as an ordinary error or successful action. The panel is shared UI, not a
separate navigation page. Tangent owns the interaction and participant
decision; Tachyon reads the host's correlated status.

## Where asks appear

| Surface | Capability gates for the affected reads/actions |
| --- | --- |
| Work task details, shared by Tasks and Board | `work_read`; `work_assign` with `agent_list`; `work_transition`; `work_comment` |
| Work Board columns, search and agent names | `work_list`; `work_search`; `agent_list` |
| Work Tasks list, search and agent names | `work_list`; `work_search`; `agent_list` |
| Launch list, details and status | `launch_list`; `launch_read`; `launch_status` |
| Launch preparation, execution and cancellation | `launch_prepare` with `agent_list`; `launch_execute`; `launch_cancel` |
| Settings target list, values, Save and Reset | `config_list`; `config_get`; `config_set`; `config_reset` |
| Observe reads and polling descriptors | `observe_activity`; `observe_events`; `observe_logs`; `observe_metrics`; `observe_status`; `observe_subscribe` |

Each control still requires its own capability. The panel itself does not
declare a plugin verb or grant additional actions. This is not universal ask
handling for every page or arbitrary plugin UI.

Work detail selections and comment drafts, Launch wizard inputs and Settings
draft values are retained when those forms receive an ask. The affected form's
actions remain blocked while its pending panel is present, including after a
terminal decision. Observe auto-refresh pauses for an ask. Leaving a form may
discard its local draft and tracking panel; it does not cancel the Tangent item.

## What the panel shows

The prompt, requested options and context are read-only. Context is escaped
text, not executable HTML. Item ID and tracking expiry are shown when supplied.
If the host supplies an absolute HTTP(S) item URL without embedded credentials,
**Open in Tangent** opens a new tab with `noopener noreferrer`. There is no
link when a safe URL is unavailable. The panel never calls Tangent APIs or
submits a participant decision directly.

**Approved** requires a host projection with state `resolved`,
`approved: true` and decision `approved`. Denied and acknowledged attention
outcomes are distinct. Canceled, expired, failed, superseded, unknown and
unavailable outcomes never imply approval. The client checks operation identity
and, when the ask supplies an item ID, requires that ID to match the host reply.

## Tracking bounds and errors

Only the same-origin host endpoint
`GET /api/hitl/operations/{operation_id}?wait_ms=25000` is polled. Requests do
not overlap within a panel. Successful waiting responses leave at least three
seconds before the next GET. Busy or unavailable responses use exponential
backoff capped at 30 seconds; five consecutive failures stop polling.

Tracking stops on terminal or unknown states, a 410 response (**No longer
tracked**), a 403 response (**Status access rejected**), or the earlier of the
ask expiry and a ten-minute mounted tracking window. Leaving the panel aborts
its browser wait. Missing operation identity or an unavailable ask shows
**Host tracking unavailable**. Host restarts, plugin replacement or expired
correlation can end tracking without resolving or canceling the Tangent item.

## Approval does not execute the action

Every outcome states that continuation is unavailable and no action was taken
by Tachyon in response to the decision. There is no **Continue**, **Resume** or
**Retry-write** control, no original-verb replay and no automatic execution
after approval. Status polling reads correlation only. Safe continuation is a
follow-up, not a supported workflow.

Starting a new action is a distinct operator intent. It does not resume the
approved operation, make repeated writes idempotent or establish whether an
earlier ambiguous write completed. Closing the panel does not withdraw the
Tangent item.

Source: [shared panel](../../frontend/src/components/pending-approval.tsx),
[host status client](../../frontend/src/api/hitl.ts), and
[host bridge contract](../hitl-host.md).
