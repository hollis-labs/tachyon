# Source Control pages

Open **Repositories** or **Activity** in the shell's **Source Control** group.
The registered page routes are `/scm` and `/scm/activity`; these are shell
navigation identifiers, not standalone browser deep links. The UI is served
under `/sysop/`.

## Capabilities

Navigation appears only when the page is registered and its required verb is
available. Missing optional verbs leave their sections unavailable.

| Surface | Required verb |
| --- | --- |
| Repositories page and repository selector | `scm_list` |
| Selected repository details and local branches | `scm_read` |
| Independent status read and refresh | `scm_status` |
| Read-only diff | `scm_diff` |
| Activity page and commit history | `scm_activity` |

## Repositories

The list contains the names, IDs and paths returned by the configured local
Git adapter. Selecting a repository reads its details lazily. Status shows
branch, HEAD, upstream, ahead/behind counts from local refs, working-tree
cleanliness and changes. If `scm_status` is absent but `scm_read` is available,
the details response supplies status without an independent refresh control.

The diff offers **Unstaged**, **Staged** and **Commits** comparisons. A commit
comparison requires a base revision; an empty head revision uses HEAD. Patch
text is rendered literally, with a display cap of 2,000 lines and 128 KiB of
UTF-8 bytes. A truncated patch reports both shown and total line/byte counts.
This is a display limit, not a promise that the backend response is bounded
to the same size.

## Activity

Choose a repository to read local branches and commits, including subject,
author, timestamp and hash. Commit limits are 20, 50 or 200. When `scm_activity`
is available without `scm_list`, the page instead offers a plain **Repository
ID** input. IDs are relative to the configured repository root, including `.`
for the root itself. The input is submitted verbatim; it does not browse the
filesystem or bypass backend confinement.

## Limits and failures

All five SCM verbs are reads. These pages do not stage, commit, checkout,
fetch, pull, push, merge or modify repository contents. There is no remote
hosting or pull-request integration. Ahead/behind counts reflect local refs,
which may be stale relative to a remote. CI is always shown as **Unknown -
local Git only**; a clean tree does not imply passing CI.

Reads show loading, empty and error states, with explicit refresh/retry
controls. Backend confinement and Git errors remain visible. Replies from
an earlier repository or comparison cannot replace the current selection.
There is no automatic polling.

Source: [page implementation](../../frontend/src/pages/scm.tsx),
[SCM API and display bounds](../../frontend/src/api/scm.ts), and
[plugin capabilities](../../plugins/scm-ops/capabilities.json).
