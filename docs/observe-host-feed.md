# Host Observe feed

The host records other plugins' operation starts and results, including `ok`,
`ask`, and `error`, and ordered load/restart/unload/admission-failure/retirement
records. Public `observe_*` verbs remain reads. Observe reads and recorder
delivery are excluded from operation instrumentation; Observe's own init/load
records remain local. Dependency probes and Nanite session snapshots still run
on demand, with their existing honest unknown counts.

Records contain host-generated identity/correlation, sequence, timestamp,
plugin/process generation, declared module/verb/effect, classified status and
reason (including precise spawn/settings/handshake/admission classes), and
duration. Duration includes waiting for the serial wire. Legacy
custom RPCs have a fixed method classification and `effect: "unknown"`; the
host does not infer a provider's effects. Unknown envelope statuses are recorded
as `unknown`. Payloads, results, prompts, configuration values, user trace IDs,
credentials, paths, and upstream error bodies are never copied into records.
Malformed/overlong identity strings become opaque hashes; process generations
are opaque random IDs. Correlation does not grant permission or continuation.

Delivery uses the supported SDK `command/execute` carrier with a private
`tachyon_host_observe` command, **not** a public ingestion verb or a new wire
method. A random per-process marker is injected into the Observe init config
only, overwritten by the host on every spawn. It never enters settings,
registry data, logs or recorded metadata. The Observe plugin requires that
marker and validates a strict, bounded DTO before recording anything. The
command is absent from capabilities/manifests; admission rejects declarations
of it. Public manager calls and HTTP command proxies refuse it, and
`POST /api/verb/tachyon_host_observe` returns `unknown_verb` (404). No generic
HTTP command route exposes it. This is an internal process marker within the
existing trusted-local deployment, not general HTTP authentication.

Recording only appends under its own short-held lock: an operation queue holds
256 records and an isolated lifecycle queue holds 64. Operation floods cannot
consume lifecycle capacity. The worker merges both queues by sequence into FIFO
batches of up to 32 records (at most 64KiB), retaining at most one such batch.
It waits up to 30 seconds for the serial pipe, then starts a fresh two-second
I/O budget only after acquiring it. It releases recorder and manager
locks before acquiring the exact captured Observe subprocess pipe. No producer
waits for delivery. Each queue drops its newest record on its own overflow;
delivered sequence
gaps therefore honestly represent loss. Missing/dead Observe drops dequeued
records; an I/O failure or ambiguous delivery drops its batch with **no retry**.
A queue-wait timeout proves nothing was written: the worker retains its frozen
batch and makes another bounded queue attempt, counting attempts rather than
record loss. Worker shutdown cancels a queue wait immediately.
A hung active ingestion is bounded by its watchdog and retires only that
captured Observe process. A timeout waiting for Observe's busy wire does not
kill the active call. Other plugins' operations and write semantics are
unchanged; no verb or write is replayed.

`observe_status.data.host_feed` adds the host epoch, last received sequence,
and cumulative record counters: `overflow` (operations), `lifecycle_overflow`,
`unavailable`, `delivery_failed`, and `delivered`. `queue_wait_timeouts` counts
pre-I/O attempts without marking records lost. Snapshots piggyback on the next
nonempty batch; no counters-only RPC is sent, so idle snapshots can lag host
accounting. These are not provider activity counts. Delivery warnings are
limited to one reason-class-only line per 30 seconds. Before Observe first
loads, the bounded queue retains
early lifecycle records. Once started, the worker remains available across
Observe restarts and reports losses when the consumer returns and more records
arrive. The worker stops with manager shutdown. Observe unloads last, but
shutdown does not wait for telemetry drain: tail records may be lost.

Both host queue and Observe's existing 1000-entry rings are ephemeral. Records
lost during overload, restart, absence or shutdown have no durable replay;
there is no completeness or exactly-once guarantee. A batch accepted before a
lost acknowledgment may be visible even though the host counts it as failed.
This change does not add a frontend feed view, external logs/history, tracing,
provider writes, durable storage, or HITL continuation.

Health thresholds and dependency probes retain their existing interpretation.
`error_count` counts local error events and host `plugin_failure` records;
`plugin_retire` does not count the same failure again. Ordinary operation errors
never change health: additive `operation_error_count` reports their count in the
current bounded event ring instead. Both counts are ephemeral ring counts.

To prevent recursive recording, any trusted plugin declaring module `observe`
is excluded from instrumentation, as is the standard `observe-ops` ID. Only the
standard Observe subprocess receives private feed delivery. Internal
`config_schemas` housekeeping is excluded; public config verbs are still
recorded with their declared identities/effects. Public `CallPlugin` rejects all
`plugin/*` lifecycle methods; only host-owned lifecycle paths may initialize,
load or unload a subprocess.
