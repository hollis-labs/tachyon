# Work Board

Open **Board** in the shell's **Work** group. Its registered page route is
`/work/board`, a shell navigation identifier within the UI served under
`/sysop/`. **Tasks** at `/work` remains a separate list page; its provider search
uses the same 200-match cap and notice described below.

## Capabilities

| Surface or action | Required verbs |
| --- | --- |
| Board navigation and columns | `work_list` |
| Search input and provider search | `work_search` |
| Open a card's task details | `work_read` |
| Resolve assignee names | `agent_list` |
| Assign an agent in task details | `work_assign` and `agent_list` |
| Explicit status change | `work_transition` |
| Add a comment | `work_comment` |

Without `work_read`, cards remain visible but cannot open details. Without
agent names, assignee IDs remain visible. Disabled agents are excluded from
new assignment choices. Provider errors are displayed rather than treated as
successful updates.

## Columns and counts

The active columns use Torque's vocabulary: `backlog`, `todo`, `queued`,
`doing`, `review`, `blocked` and `paused`. Closed columns are `done`, `archived`,
`abandoned` and `cancelled`. Closed columns initially show counts only;
**Show closed** expands them into cards, and **Hide closed** collapses them.

Each column makes its own server-side status-filtered query. Expanded columns
request 50 tasks per page; **Load more** follows that column's `next_offset`.
Collapsed closed columns request one task to obtain the count, without
rendering cards. The board does not fetch the entire task collection first.

Torque's filtered `work_list.total` describes the matching status/project
cohort, rather than just the returned page. A structurally credible total is
shown as a total; absent or invalid totals use **at least N** for the observed
lower bound. A loaded-card count is separate from the total. Offset pages are
not an atomic snapshot: concurrent provider changes can affect pagination.
Duplicate task IDs are combined when appending pages.

The **Project ID** field applies to column queries. With no explicit project,
the adapter uses its configured default project scope. **Refresh** reloads
columns; the board does not automatically poll.

## Search and task actions

Search uses `work_search`, independently of the board columns. The project
filter does not apply to search. Provider-custom statuses appear only in
search; the board cannot discover all custom statuses without scanning the
whole collection. Search returns at most 200 matches. When a consistent exact
provider total exceeds the returned count, Tasks and Board show:
**Showing the first 200 of N matches. Refine your search.** An exactly-200
complete result has no cap notice. Missing or inconsistent metadata does not
produce an invented total. Search has no pagination control or automatic
loading; refine the query to narrow the results. Counts describe returned
results, and Tasks' local status/assignee filters do not change the provider's
search total.

Cards show title, ID, status, priority and assignee. Their details dialog is
shared with the Tasks page. Assignment, status changes and comments require
explicit controls. The status selector includes known Torque statuses and the
task's current status; it does not infer a valid transition graph. The provider
can reject a selected transition. There are no drag-and-drop writes, task
creation controls or general task-editing form on the board.

## Failures and operator decisions

Columns distinguish loading, empty results and failures. A failed additional
page retains already loaded cards and exposes Retry; it is not rendered as an
empty column. Stale replies from an earlier filter are ignored.

An `ask` response is shown through the shared [operator-decision
panel](hitl-ui.md). Task detail drafts and selections stay in place while that
form's actions are blocked. Approval does not replay the original operation.

Source: [board implementation](../../frontend/src/pages/work-board.tsx),
[column API and statuses](../../frontend/src/api/work-board.ts),
[shared task details](../../frontend/src/components/work/work-detail.tsx), and
[plugin capabilities](../../plugins/work-ops/capabilities.json).
