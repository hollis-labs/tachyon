# Host Observe feed

The host records other plugins' operation starts and results, including `ok`,
`ask`, and `error`, and ordered load/restart/unload/admission-failure/retirement
records. Public `observe_*` verbs remain reads. Observe reads and recorder
delivery are excluded from operation instrumentation; Observe's own init/load
records remain local. Dependency probes and Nanite session snapshots still run
on demand, with their existing honest unknown counts.

Records contain host-generated identity/correlation, sequence, timestamp,
plugin/process generation, declared module/verb/effect, classified status and
reason, and duration. Duration includes waiting for the serial wire. Legacy
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

Recording only appends to a 256-record queue under its own short-held lock.
A separate worker delivers FIFO batches of up to 32 records (at most 64KiB)
with a one-second total queue/I/O budget. It releases recorder and manager
locks before acquiring the exact captured Observe subprocess pipe. No producer
waits for delivery. Queue overflow drops the newest record; delivered sequence
gaps therefore honestly represent loss. Missing/dead Observe drops dequeued
records; a failed or ambiguous delivery drops its batch with **no retry**.
A hung active ingestion is bounded by its watchdog and retires only that
captured Observe process. A timeout waiting for Observe's busy wire does not
kill the active call. Other plugins' operations and write semantics are
unchanged; no verb or write is replayed.

`observe_status.data.host_feed` adds the host epoch, last received sequence,
and cumulative record counters: `overflow`, `unavailable`, `delivery_failed`,
and `delivered`. Counter snapshots follow successful delivery; they are not
provider activity counts. Before Observe first loads, the bounded queue retains
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
