# Host operations and deployment

This describes the merged host chain through PR #47 (`47af42b`), not a claim that
that revision is deployed. Named constants in `internal/plugins/watchdog.go`,
`internal/plugins/observe.go`, and `cmd/tachyon/{plugins_discover,shutdown}.go`
are authoritative. Provider behavior and frontend pages have separate contracts.

## Wire and deadline model

Each subprocess has a single stdin/stdout JSON-RPC stream, a serial call lock,
and a persistent decoder. Request IDs do not enable concurrent wire calls.
Declared verbs travel through plugin-sdk v0.4.0 `command/execute`, with the verb
name and JSON payload string; the host unwraps the result envelope. No native
`verb/invoke` transport is used.

| Stage | Host ceiling | Meaning |
| --- | --- | --- |
| Ordinary call queue | 120s, or earlier caller cancellation/deadline | Waiting never interrupts the active owner |
| Ordinary active I/O | Fresh 120s after acquiring the wire | Request cancellation/deadline is dropped; context values remain |
| Init/load/capability discovery | 120s per I/O stage | Lifecycle-owner context may shorten the budget |
| Restart/shutdown lifecycle-lock acquisition | 120s, or earlier owner deadline | Restart busy leaves the current plugin unchanged |
| Plugin unload | 5s per plugin | Force kill/close/reap after grace |
| Startup rollback | Independent shared 5s cleanup context | Expired startup context cannot skip cleanup |
| HTTP shutdown drain | Independent 5s, before plugin unload | Close remaining connections if drain expires |
| Observe ingestion queue / I/O | 30s / fresh 2s | Queue timeout retains unwritten batch; ambiguous I/O is never replayed |

Queue time does not consume the active I/O allowance. Ordinary calls can thus
wait up to 120s and then use another 120s; startup is not one aggregate 120s
budget across every plugin/handshake. Browser disconnects after dispatch miss
the result but leave active work running to completion or its host watchdog.
Owner-controlled init/load/unload/shutdown contexts retain cancellation for I/O.

The 120s ceiling was audited above the supported finite provider sequences:

| Operation/provider | Inner budget | Host interpretation |
| --- | --- | --- |
| `service_health` | Up to three serial 30s requests (90s) | 120s leaves headroom |
| `work_assign` | Up to two serial 30s requests (60s) | 120s leaves headroom |
| Other finite HTTP provider operations | At most 30s per request; Tether launch HTTP at most 20s | Host remains the outer wire ceiling |
| Local SCM | Up to 33s subprocess budget | Host remains the outer wire ceiling |
| Agent Ops HTTP, filesystem/SQLite work | No finite provider ceiling | Host interruption bounds transport waiting, not upstream completion |

There is **no finite provider ceiling** for every operation. Killing a plugin
does not prove that an upstream write was canceled or rolled back. The host
never automatically retries an ambiguous plugin write. Local marshal failures
happen before any write; a fully consumed but wrongly shaped response fails that
call without discarding an otherwise synchronized stream. I/O/syntax failures or
watchdog expiry interrupt only the exact captured process: close pipes, kill,
reap and retire. Next calls fail promptly; other plugins keep their own wires.

Retirement is asynchronous outside callMu. Its lifecycle-lock wait can delay
reaping an already killed child while a bounded load holds that lock. Identity
checks prevent a late watchdog/retirement from killing or detaching a replacement.

## Admission before HTTP startup

The host reads `./plugins/` relative to its working directory. Manifest-bearing
directories load in sorted order with binary `plugins/<id>/<id>`; optional,
manifest-free hello loads first. Present executables must initialize, load and
complete capability discovery before any HTTP listener opens.

| Condition | Startup decision |
| --- | --- |
| Missing plugins directory or binary | Warning; optional absence |
| Unbuilt/non-executable binary | Warning with build guidance; optional absence |
| Init/load/handshake timeout | Refuse startup; 120s host I/O ceiling per stage |
| Discovery RPC rejection or command `action:"error"` | Warning; admit legacy without module claims |
| Malformed declaration or RPC/command shape | Refuse startup |
| Module/verb ownership collision | Refuse startup |
| Invalid nav/settings declaration or persisted settings | Refuse startup |
| Hello/no-capabilities | Absence is optional; explicit discovery rejection admits legacy |

Unexpected filesystem failures, spawn/load failures and unexpected discovery
command actions also refuse startup. Cross-plugin nav group/item IDs follow
ADR 001's first-loaded precedence; invalid local references are hard errors.

A refusal exits nonzero and emits one structured ERROR `plugin startup refused`
with `plugin_id`, `path`, and `reason_class` (plus diagnostics). No listener was
opened. The rejected child is force-stopped/reaped, and already loaded children
are unloaded within their independent shared 5s rollback grace, then force-stopped
and reaped. Cleanup may emit its own log lines. An explicit runtime restart
failure affects that plugin, not startup admission for the whole host.

## Retirement, registry and recovery

Retirement removes module/verb/nav/bundle routing and preserves a small in-memory
tombstone: id, name, executable path, process-owner lifetime, reason class and
retirement time. It never stores a request context or keeps the broken stream.
Graceful unload creates no tombstone. Successful load clears it; shutdown clears
all recovery metadata. Host restart reconstructs plugins from the filesystem.

`GET /api/plugins/settings` lists live schemas and retired targets. Absent state
means loaded, preserving the existing loaded shape. Retired targets carry `id`,
`name`, `state:"unloaded"`, `reason`, `retired_at` and an empty `settings` object without schema fields or routing claims.
`GET /api/plugins/registry` adds optional `retired_plugins` with the same safe
metadata. Consumers must tolerate additive fields; the regular loaded registry
continues to describe loaded plugins only.

`POST /api/plugins/{id}/restart` is explicit operator recovery, using either the
live process or its retired tombstone. Persisted settings apply to the new spawn.

| Result | HTTP / status |
| --- | --- |
| Successful load | 200 / `loaded`; tombstone cleared |
| Lifecycle lock busy (`ErrRestartBusy`) | 503 / `unchanged`; restart not attempted, tombstone retained |
| Unknown id (`ErrPluginNotFound`) | 404 / `unloaded` |
| Actual respawn/admission failure | 500 / `unloaded` |

A valid restart does not retry the interrupted operation. Recovery UI details
are reserved for the final page documentation pass.

## Local settings and shutdown

config-ops is the sole writer of `<data root>/config-ops/settings.json`. The data
root is `TACHYON_DATA_DIR`, otherwise absolute `XDG_DATA_HOME/tachyon/plugins`,
otherwise `$HOME/.local/share/tachyon/plugins`. Authored settings declarations
filter and validate persisted overrides before they enter plugin init. Saving or
resetting a setting returns `restart_required`; it is not a live configuration
change. Plugin installation, enable/disable and provider state storage are not
host configuration features.

SIGINT/SIGTERM first stops accepting HTTP and drains existing requests for 5s.
On timeout, the host logs stage `http_drain` and closes connections. Plugin
shutdown then uses fresh per-plugin 5s unload budgets, independent of the HTTP
budget. Hung hooks are force-stopped/reaped; later plugins still get their grace.
Logs identify `plugin_unload` and the plugin, or `plugin_shutdown` stage failures.
Observe unloads last, without waiting for telemetry drain. A clean signal stop
exits zero.

The deployed systemd user service has `TimeoutStopSec=90s`. Nine loaded binaries
(eight modules plus hello) need at most 5s + nine 5s unload graces in the ordinary
shutdown path, excluding lifecycle-lock waiting and process-reaping overhead.
The 120s lifecycle wait can exceed systemd's 90s limit under contention; do not
interpret those inner budgets as an aggregate guarantee against systemd's final
kill. Startup rollback intentionally retains its shared 5s cleanup cap.

## Optional HITL and Observe

Tangent is disabled when `TACHYON_TANGENT_MCP_URL` is unset: the plugin ask bytes
pass through unchanged, with no bridge network traffic. Configured asks are
validated before enqueue. Read-only, same-origin host status returns a safe
projection with authoritative approval, bound to host epoch, operation, exact
plugin generation, verb and canonical payload digest. Correlation is bounded to
256 entries, 32KiB payloads and 10 minutes; restart/loss/expiry fails closed.
**Continuation is unavailable**: no resume POST, verb redispatch or automatic
write retry. CW-20261001-0532 owns the continuation design. See [HITL](hitl-host.md)
for exact caps, safe links, singleflight and same-request enqueue retry semantics.

Observe delivery is private: an in-band per-process marker authorizes
`tachyon_host_observe`, absent from public capabilities/registry/verb listings.
HTTP verb and command routes cannot inject records; public manager calls reject
private ingestion and plugin lifecycle methods. Records contain safe identity,
status, declared effect and duration, never payloads/prompts/config/credentials
or upstream error bodies. Producers append to 256 operation slots and 64 isolated
lifecycle slots; a worker batches up to 32. Queue waiting is 30s, then fresh 2s
I/O. Pre-I/O queue timeout retains the batch; ambiguous delivery is dropped.
Counters distinguish operation/lifecycle overflow, unavailable, delivery_failed,
delivered, and queue_wait_timeouts. Snapshots piggyback on a later batch.
Operation errors have a separate counter and do not alter health; lifecycle
failure and dependency-probe health semantics remain intact. [Observe feed](observe-host-feed.md)
documents recursion exclusions, warning limits and ephemeral retention.

## Deployment: source, artifacts and runtime

The checked-in `tachyon.cerberus.yaml` defines `tachyon-backend-dev` as a local
process in `os_service` mode, supervisor auto, service `tachyon`,
`run_from:workspace`. It builds with make_standard target `all`, output `tachyon`,
and sets `install_after_build:false`. This override matters: Cerberus normally
runs its install-after-build step by default; `--install-after-build` forces it
and `--no-install-after-build` skips it for that invocation. `make install`
rebuilds UI/plugins then installs only the host through go install; it is not a
plugin distribution package.

Read-only inspection on 2026-10-01 confirmed the systemd user service executes
`/home/chrispian/dev/hollis-labs/apps/tachyon/tachyon` with that same working
directory. Cerberus also tracks an artifact/install layout under `.cerberus`,
but this workspace-based service does not execute that stored artifact path.
Relative plugin paths are why the working directory must contain built plugins.

The deployed resource sets `TACHYON_ADDR=127.0.0.1:8093`. Caddy binds the Tailscale
interface and routes `https://tachyon.nanite.cloud/sysop/` to loopback 8093,
redirecting `/` to `/sysop/` and terminating HTTPS. This is a deployment override,
not a changed code default or new authentication policy. CW-20261001-0476's
listener decision is not resolved by this documentation.

Read-only operator inspection:

```sh
cerberus resource status tachyon-backend-dev
cerberus resource inspect tachyon-backend-dev
systemctl --user show tachyon.service -p ExecStart -p WorkingDirectory -p TimeoutStopUSec
```

A merged commit, a freshly built binary, and a restarted running service are
three different facts. Record the selected revision/build and runtime start;
health OK alone does not prove every plugin/provider healthy or identify the
running revision. Do not reset/build/clean the live workspace as a development
shortcut.

Before an authorized reload, pre-flight the exact candidate in an isolated
worktree with built candidate plugins, temporary HOME/data and an unused
loopback port (never the live 8093 port). Fake providers are required for mutation
tests; any real-provider pre-flight is read-only and explicitly scoped. Check
admission, safe read routes and graceful exit without changing live state.

After operator authorization and review of the workspace revision, the deployment
command is `cerberus resource deploy tachyon-backend-dev --ack` (build and apply).
`resource apply ... --ack` applies an already-built artifact; `resource reload
... --ack` restarts without rebuilding and cannot fix a stale binary. Do not use
remove as a stop operation. These commands are documented here, not executed by
a documentation change. Rollback means the previously deployed revision and its
matching UI/plugins/host artifacts, then an authorized deploy/reload and read-only
verification; reverting source alone does not roll back the running process or
provider data.

## Known limits and final documentation pass

- No HTTP authentication/authorization, built-in TLS, plugin sandbox or binary
  signatures. Same-origin status and private markers do not replace those.
- No finite ceiling on all upstream work; interrupted writes can have unknown
  completion. Caller disconnects during active I/O do not cancel them.
- Startup budgets are per stage; lifecycle contention can outlast systemd's stop
  limit. Reaping can wait behind bounded load; portability tests use Unix
  syscall.Kill and some timing-sensitive windows.
- Retired recovery and HITL correlation are in-memory. No durable continuation
  or provider rollback; orphaned Tangent items expire under Tangent ownership.
- Observe rings/queues are lossy and ephemeral; idle counters can lag, shutdown
  tails can be lost, and trusted plugins claiming observe are excluded to prevent
  recursion. External history/streams and exactly-once delivery are unavailable.
- Local settings are plaintext, applied at restart; no plugin install or
  enable/disable surface. See SECURITY.md for provider and log protection.

**CW-20261001-0496 final-pass placeholders:** reconcile Observe UI (0492),
Work/Board per-status behavior, Services/SCM/Settings recovery, Toaster and search
cap after their corresponding frontend PRs merge. Do not infer page implementation
from this host contract. These placeholders will be replaced with source-verified
page behavior in the later frontend documentation theme.
