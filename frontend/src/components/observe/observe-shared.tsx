import { Button, EmptyState, Input, Label, Skeleton } from "@hollis-labs/design-components"
import { ListPageLayout } from "@hollis-labs/kit-dashboard/layout"
import { PageHeader } from "@hollis-labs/kit-dashboard/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { PendingApprovalError } from "../../api/hitl"
import {
  type HostFeedStatus,
  observeApi,
  observeData,
  type PollChannel,
  type SnapshotFilter,
  type StatusSummary,
  validateDescriptor,
} from "../../api/observe"
import { type AskDetail, useVerbs } from "../../api/verbs"
import { PendingApproval } from "../pending-approval"

export const selectClass = "h-9 max-w-full rounded-md border border-border bg-surface px-2 text-sm"
export const timeText = (value: string | undefined) =>
  value && Number.isFinite(Date.parse(value))
    ? new Date(value).toLocaleString()
    : "Timestamp unavailable"
const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error))
const readQueues = new Map<string, Promise<unknown>>()

interface ReadState<T> {
  data?: T
  loading: boolean
  error: string
  ask?: AskDetail
  updated?: string
}

// Queues survive filter changes and remounts, so the same channel never overlaps.
// Superseded queued work is skipped; old replies cannot change the next view.
export function useObserveRead<T>(
  enabled: boolean,
  load: () => Promise<T>,
  polling?: { channel: PollChannel; filter: SnapshotFilter; enabled: boolean },
) {
  const [state, setState] = useState<ReadState<T>>({ loading: enabled, error: "" })
  const [revision, setRevision] = useState(0)
  const [auto, setAuto] = useState(false)
  const [notice, setNotice] = useState("")
  const deadline = useRef(0)
  const autoRef = useRef(false)
  autoRef.current = auto
  const channel = polling?.channel
  const filter = polling?.filter
  const canPoll = polling?.enabled ?? false
  const queueKey = channel ?? "direct"
  const serialize = useCallback(
    <R,>(alive: () => boolean, request: () => Promise<R>) => {
      const pending = (readQueues.get(queueKey) ?? Promise.resolve())
        .catch(() => {})
        .then(() => (alive() ? request() : undefined))
      readQueues.set(queueKey, pending)
      return pending
    },
    [queueKey],
  )
  // biome-ignore lint/correctness/useExhaustiveDependencies: revision deliberately triggers an explicit manual read.
  useEffect(() => {
    let active = true
    if (!enabled) {
      setState({ loading: false, error: "" })
      return () => {
        active = false
      }
    }
    if (autoRef.current)
      return () => {
        active = false
      }
    setState((old) => ({ ...old, loading: true, error: "" }))
    void serialize(() => active, load)
      .then((data) => {
        if (active) setState({ data, loading: false, error: "", updated: new Date().toISOString() })
      })
      .catch((error) => {
        if (active)
          setState((old) => ({
            ...old,
            loading: false,
            error: error instanceof PendingApprovalError ? "" : errorText(error),
            ask: error instanceof PendingApprovalError ? error.ask : undefined,
          }))
      })
    return () => {
      active = false
    }
  }, [enabled, load, revision, serialize])
  useEffect(() => {
    if (!auto) return
    if (!enabled || !canPoll || !channel || !filter) {
      setAuto(false)
      setNotice("Auto-refresh paused: required capability unavailable.")
      return
    }
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    let bound: ReturnType<typeof setTimeout> | undefined
    let failures = 0
    const alive = () => active && Date.now() < deadline.current
    const stop = (reason: string) => {
      if (!active) return
      active = false
      clearTimeout(timer)
      clearTimeout(bound)
      setAuto(false)
      setNotice(reason)
      setState((old) => ({ ...old, loading: false }))
    }
    const remaining = deadline.current - Date.now()
    if (remaining <= 0) {
      stop("Auto-refresh paused after ten minutes.")
      return
    }
    bound = setTimeout(() => stop("Auto-refresh paused after ten minutes."), remaining)
    async function start() {
      try {
        const descriptor = await serialize(alive, async () =>
          validateDescriptor(
            observeData(
              await observeApi.subscribe(channel as PollChannel, filter as SnapshotFilter),
            ),
            channel as PollChannel,
          ),
        )
        if (!alive() || !descriptor) return
        const contract = descriptor
        async function poll() {
          if (!alive()) return
          setState((old) => ({ ...old, loading: true }))
          try {
            const data = await serialize(alive, async () =>
              observeData(
                await observeApi.snapshot<T>(contract.channel as PollChannel, contract.payload),
              ),
            )
            if (!alive()) return
            failures = 0
            setState({ data, loading: false, error: "", updated: new Date().toISOString() })
          } catch (error) {
            if (!alive()) return
            setState((old) => ({
              ...old,
              loading: false,
              error: error instanceof PendingApprovalError ? "" : errorText(error),
              ask: error instanceof PendingApprovalError ? error.ask : undefined,
            }))
            if (error instanceof PendingApprovalError) {
              stop("Auto-refresh paused for an operator decision.")
              return
            }
            failures++
            if (failures >= 5) {
              stop("Auto-refresh paused after five failed reads.")
              return
            }
          }
          if (alive())
            timer = setTimeout(
              () => {
                void poll()
              },
              Math.max(contract.poll_interval_ms, Math.min(30000, 2000 * 2 ** failures)),
            )
        }
        void poll()
      } catch (error) {
        if (!active) return
        if (error instanceof PendingApprovalError) {
          setState((old) => ({ ...old, ask: error.ask, loading: false }))
          stop("Auto-refresh paused for an operator decision.")
        } else stop(errorText(error))
      }
    }
    void start()
    return () => {
      active = false
      clearTimeout(timer)
      clearTimeout(bound)
    }
  }, [auto, canPoll, enabled, channel, filter, serialize])
  return {
    ...state,
    auto,
    notice,
    canPoll,
    refresh: () => {
      if (!state.ask && !auto) setRevision((value) => value + 1)
    },
    toggleAuto: (value: boolean) => {
      if (state.ask) return
      if (value) {
        deadline.current = Date.now() + 600000
        setNotice("")
      } else {
        setNotice("Auto-refresh paused.")
        setState((old) => ({ ...old, loading: false }))
      }
      setAuto(value)
    },
  }
}

export function ObserveLayout({
  title,
  verb,
  children,
}: {
  title: string
  verb: string
  children: ReactNode
}) {
  const verbs = useVerbs()
  return (
    <ListPageLayout header={<PageHeader title={title} />}>
      <div className="min-w-0 space-y-4 p-4">
        <p className="text-sm text-text-muted">
          Current snapshots and retained host records; not durable history.
        </p>
        {verbs.loading ? (
          <Skeleton className="h-24" />
        ) : !verbs.has(verb) ? (
          <EmptyState
            variant="empty"
            title="Observe unavailable"
            description={
              verbs.available === true
                ? "The required read verb is unavailable."
                : "Could not discover Observe capabilities."
            }
          />
        ) : (
          children
        )}
      </div>
    </ListPageLayout>
  )
}
export function ReadControls<T>({ read }: { read: ReturnType<typeof useObserveRead<T>> }) {
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm">
      <Button
        variant="outline"
        disabled={read.loading || read.auto || !!read.ask}
        onClick={read.refresh}
      >
        Refresh
      </Button>
      {read.canPoll && (
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={read.auto}
            disabled={!!read.ask}
            onChange={(event) => read.toggleAuto(event.target.checked)}
          />
          Auto-refresh snapshots
        </label>
      )}
      {!read.canPoll && <span>Manual reads only</span>}
      {read.auto && <span role="status">Auto-refresh on (ten-minute bound)</span>}
      {read.notice && (
        <span role="status" className="break-words">
          {read.notice}
        </span>
      )}
      {read.updated && <span>Last successful read: {timeText(read.updated)}</span>}
    </div>
  )
}
export function ReadRegion<T>({
  read,
  children,
}: {
  read: ReturnType<typeof useObserveRead<T>>
  children: ReactNode
}) {
  return (
    <div className="min-w-0 space-y-3">
      {read.ask && <PendingApproval ask={read.ask} />}
      {read.error && (
        <p role="alert" className="break-words text-status-failed">
          {read.data !== undefined ? "Retained snapshot is stale. " : "Read failed. "}
          {read.error}
        </p>
      )}
      {read.loading &&
        (read.data === undefined ? (
          <Skeleton className="h-24" />
        ) : (
          <p role="status">Refreshing; previous snapshot retained.</p>
        ))}
      {read.data !== undefined && children}
    </div>
  )
}
export function Details({ value }: { value: unknown }) {
  if (value === undefined || value === null) return null
  return (
    <details>
      <summary className="cursor-pointer text-sm">Details (read only)</summary>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  )
}
export function FilterInput({
  name,
  value,
  change,
  type = "text",
}: {
  name: string
  value: string
  change: (value: string) => void
  type?: string
}) {
  const id = `observe-${name.toLowerCase().replaceAll(" ", "-")}`
  return (
    <div className="min-w-0">
      <Label htmlFor={id}>{name}</Label>
      <Input id={id} type={type} value={value} onChange={(event) => change(event.target.value)} />
    </div>
  )
}
export function limitValue(value: string): number {
  const limit = Number(value)
  if (!Number.isInteger(limit) || limit < 1 || limit > 200)
    throw new Error("Limit must be an integer from 1 to 200.")
  return limit
}
export function dateFilters(since: string, until: string) {
  const from = since ? Date.parse(since) : undefined
  const to = until ? Date.parse(until) : undefined
  if ((from !== undefined && !Number.isFinite(from)) || (to !== undefined && !Number.isFinite(to)))
    throw new Error("Enter valid dates.")
  if (from !== undefined && to !== undefined && from > to)
    throw new Error("From must not be after Until.")
  return {
    ...(from === undefined ? {} : { since: new Date(from).toISOString() }),
    ...(to === undefined ? {} : { until: new Date(to).toISOString() }),
  }
}
const counterText = (value: unknown) =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? String(value)
    : "Unavailable"
function HostReceipt({ receipt }: { receipt: HostFeedStatus | undefined }) {
  if (!receipt) return <p>No host-feed receipt reported</p>
  const counters = receipt.counters
  const lost = [
    counters?.overflow,
    counters?.lifecycle_overflow,
    counters?.unavailable,
    counters?.delivery_failed,
  ].some((value) => typeof value === "number" && value > 0)
  return (
    <section aria-label="Host-feed receipt" className="space-y-2">
      <h3 className="font-medium">Host-feed receipt</h3>
      <p>Delivery counters are the last received report and may lag host accounting.</p>
      <dl className="grid grid-cols-1 gap-2 break-words sm:grid-cols-2">
        <dt>Host epoch</dt>
        <dd className="break-all">{receipt.epoch || "Unavailable"}</dd>
        <dt>Last received sequence</dt>
        <dd>{counterText(receipt.last_sequence)}</dd>
        <dt>Delivered records</dt>
        <dd>{counterText(counters?.delivered)}</dd>
        <dt>Operation overflow records</dt>
        <dd>{counterText(counters?.overflow)}</dd>
        <dt>Lifecycle overflow records</dt>
        <dd>{counterText(counters?.lifecycle_overflow)}</dd>
        <dt>Unavailable records</dt>
        <dd>{counterText(counters?.unavailable)}</dd>
        <dt>Failed-delivery records</dt>
        <dd>{counterText(counters?.delivery_failed)}</dd>
        <dt>Queue-wait timeout attempts (not record loss)</dt>
        <dd>{counterText(counters?.queue_wait_timeouts)}</dd>
      </dl>
      {lost && (
        <p className="text-status-failed">
          Retained telemetry may be incomplete: records were dropped or delivery failed.
        </p>
      )}
      <p className="text-text-muted">
        Counters are not provider activity or complete history. Accepted records may be visible even
        when their acknowledgment was lost. No durable replay or exactly-once guarantee.
      </p>
    </section>
  )
}
function StatusView({ status }: { status: StatusSummary }) {
  return (
    <div className="space-y-3 text-sm">
      <dl className="grid grid-cols-1 gap-2 break-words sm:grid-cols-2">
        <dt>Observe aggregate health</dt>
        <dd>{status.health_status || "Unavailable"}</dd>
        <dt>Local error events and plugin failures (retained ring)</dt>
        <dd>{counterText(status.error_count)}</dd>
        <dt>Operation errors (retained ring; separate from health)</dt>
        <dd>{counterText(status.operation_error_count)}</dd>
        <dt>Active agents</dt>
        <dd>Unknown — not measured</dd>
        <dt>Nanite sessions with status active</dt>
        <dd>
          {status.session_count_known === true &&
          Number.isSafeInteger(status.active_sessions) &&
          status.active_sessions >= 0
            ? status.active_sessions
            : "Unknown"}
        </dd>
        <dt>Observe process uptime</dt>
        <dd>
          {Number.isFinite(status.uptime_seconds) && status.uptime_seconds >= 0
            ? `${status.uptime_seconds} seconds`
            : "Unavailable"}
        </dd>
        <dt>Last updated</dt>
        <dd>{timeText(status.last_updated)}</dd>
      </dl>
      <p className="text-text-muted">
        Dependency reachability is not workload health. Active session status does not mean
        executing.
      </p>
      <HostReceipt receipt={status.host_feed} />
      <h3 className="font-medium">Dependency reachability</h3>
      {status.dependencies?.length ? (
        <ul className="space-y-2">
          {status.dependencies.map((dependency) => (
            <li key={dependency.source} className="rounded border border-border p-3 break-words">
              <span className="font-medium">{dependency.source}</span> ·{" "}
              {dependency.status || "Unavailable"}
              <p>Checked: {timeText(dependency.checked_at)}</p>
              {dependency.error && <p className="text-status-failed">{dependency.error}</p>}
            </li>
          ))}
        </ul>
      ) : (
        <p>No dependency reachability reported</p>
      )}
    </div>
  )
}
export function ObserveStatus() {
  const verbs = useVerbs()
  const load = useCallback(() => observeApi.status().then(observeData), [])
  const read = useObserveRead(verbs.has("observe_status"), load)
  if (!verbs.has("observe_status")) return <p>Status read unavailable</p>
  return (
    <section aria-label="Observe status" className="space-y-3 rounded border border-border p-4">
      <h2 className="text-lg font-medium">Observe status</h2>
      <ReadControls read={read} />
      <ReadRegion read={read}>
        {read.data ? <StatusView status={read.data} /> : <p>Status unavailable</p>}
      </ReadRegion>
    </section>
  )
}

// Providers may repeat probe IDs and metric timestamps. Identical occurrences
// have no local row state, while distinct records retain their identity.
export function keyedSnapshot<T>(rows: T[]) {
  const occurrences = new Map<string, number>()
  return rows.map((row) => {
    const identity = JSON.stringify(row)
    const count = occurrences.get(identity) ?? 0
    occurrences.set(identity, count + 1)
    return { row, key: `${identity}:${count}` }
  })
}
