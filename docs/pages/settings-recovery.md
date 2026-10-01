# Settings and plugin recovery

Settings lists plugin targets through declared config capabilities and separates
Loaded from Unloaded. Loaded targets expose their declared field schema and
validated values. String, boolean, number and select controls follow the schema;
unsupported reads/writes are disabled or explained. Save applies dirty values;
Reset restores defaults. Both persist settings for a later plugin restart rather
than changing the running process immediately.

## Explicit recovery

An unloaded target shows its ID, name, safe retirement reason and retirement
time. Configuration and plugin operations are unavailable until recovery
succeeds. Restart plugin sends one explicit host POST; it never automatically
retries the operation that caused retirement.

| Host result | Operator-visible meaning |
| --- | --- |
| 200 / loaded | Plugin recovered; refresh its host metadata |
| 503 / unchanged | Restart busy; current state retained, try later |
| 404 | Plugin no longer known; refresh the list |
| 500 / unloaded | Restart failed; plugin remains unavailable |

Restart is disabled while pending. Recovery failures remain visible inline;
a busy response preserves the retirement snapshot. Loaded settings with unsaved
changes block restart until those changes are handled. Successful restart makes
saved settings active and refreshes capabilities/targets as appropriate.

When retired entries exist and plugin navigation has no Settings route, the
shell offers **Plugin recovery**, a host-owned fallback. It reads retirement
metadata from the registry without relying on config-ops. Successful recovery
refreshes registry, navigation and verbs so the plugin pages can return.
There is no install, enable/disable or automatic restart control. Retirement
that occurs after the page loads becomes visible after a reload/refresh; this
is not a live retirement subscription.

## Pending decisions and focus

Settings reads and writes preserve an ask as a pending-decision panel. Drafts
and selection remain available; pending writes block further writes in that
form. Status tracking is read-only, and approval does not save, reset, restart
or replay the original verb. Safe continuation is unavailable; see
[HITL](../hitl-host.md).

Recovery returns focus to the restart control when it remains mounted, or to
the restored Settings/recovery navigation after the host snapshot changes.
Notices and failures use inline feedback rather than the global toast channel.
See [host operations](../host-operations.md) for persistent settings, tombstones
and restart deadlines.
