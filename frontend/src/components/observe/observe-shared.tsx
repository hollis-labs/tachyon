import { Button, Input, Label, Pill } from "@hollis-labs/design-components"
import { ListPageLayout } from "@hollis-labs/kit-dashboard/layout"
import { PageHeader } from "@hollis-labs/kit-dashboard/ui"
import {
  DiagnosticPanel,
  HealthSummary,
  type ObservationState,
  ObservationStatus,
  StatCollection,
} from "@hollis-labs/kit-observe"
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { PendingApprovalError } from "../../api/hitl"
import {
  type DependencyStatus,
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
import {
  capabilityObservation,
  diagnosticProjection,
  diagnosticSchema,
  observationState,
  reachability,
  reportedHealth,
  snapshotRows,
  statusSnapshot,
  statusStats,
} from "./observe-kit-adapter"

export const selectClass = "h-9 max-w-full rounded-md border border-border bg-surface px-2 text-sm"
export const timeText = (value: string | undefined) =>
  value && Number.isFinite(Date.parse(value))
    ? new Date(value).toLocaleString()
    : "Timestamp unavailable"
const errorText = (_error: unknown) =>
  "The Observe read or polling descriptor failed. Use an explicit Refresh to try again."
const readQueues = new Map<string, Promise<unknown>>()
const ObservationContext = createContext<ObservationState | null>(null)

function useObservationClock() {
  const [nowMs, setNowMs] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNowMs(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return nowMs
}

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
            const data = await serialize(alive, async () => {
              const snapshot = observeData(
                await observeApi.snapshot<T>(contract.channel as PollChannel, contract.payload),
              )
              snapshotRows(
                snapshot as unknown[] | null,
                contract.channel === "logs"
                  ? ["id", "timestamp", "level", "source", "message"]
                  : [
                      "id",
                      "timestamp",
                      "kind",
                      "source",
                      ...(contract.channel === "activity" ? ["summary", "actor"] : []),
                    ],
              )
              return snapshot
            })
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
  const nowMs = useObservationClock()
  return (
    <ListPageLayout header={<PageHeader title={title} />}>
      <div className="min-w-0 space-y-4 p-4">
        <p className="text-sm text-text-muted">
          Current snapshots and retained host records; not durable history.
        </p>
        {verbs.loading || verbs.available !== true || !verbs.has(verb) ? (
          <ObservationStatus
            label="Observe availability"
            observation={capabilityObservation(
              verbs.loading,
              verbs.available,
              verbs.has(verb),
              nowMs,
            )}
          />
        ) : (
          children
        )}
        {!verbs.loading && verbs.available === true && !verbs.has(verb) && (
          <p>The required read verb is unavailable.</p>
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
  label = "Snapshot received",
  observedAt,
}: {
  read: ReturnType<typeof useObserveRead<T>>
  children: ReactNode
  label?: string
  observedAt?: string
}) {
  const nowMs = useObservationClock()
  const { loading, error, updated, auto } = read
  const pending = !!read.ask
  const observation = useMemo(
    () =>
      observationState({ loading, error, updated, auto, ask: pending }, nowMs, true, observedAt),
    [loading, error, updated, auto, pending, nowMs, observedAt],
  )
  return (
    <div className="min-w-0 space-y-3">
      {read.ask && <PendingApproval ask={read.ask} />}
      <p className="text-xs text-text-muted">
        Freshness uses a 30-second host policy; age does not establish workload health.
      </p>
      <ObservationContext.Provider value={observation}>
        <ObservationStatus
          label={label}
          observation={{
            ...observation,
            onRetry: !read.ask && !read.loading && !read.auto ? read.refresh : undefined,
          }}
        >
          {read.data !== undefined && children}
        </ObservationStatus>
      </ObservationContext.Provider>
    </div>
  )
}
export function Details({ value }: { value: unknown }) {
  const observation = useContext(ObservationContext)
  const projection = useMemo(() => diagnosticProjection(value), [value])
  if (value === undefined || value === null) return null
  if (!observation) return null
  return (
    <details className="min-w-0">
      <summary className="cursor-pointer text-sm">Details (read only)</summary>
      <p className="text-xs text-text-muted">
        Recognized metadata only; other fields and raw error text are omitted.
      </p>
      <DiagnosticPanel
        label="Snapshot metadata"
        schema={diagnosticSchema}
        {...projection}
        observation={observation}
      />
    </details>
  )
}
export function FilterInput({
  name,
  value,
  change,
  type = "text",
  disabled = false,
}: {
  name: string
  value: string
  change: (value: string) => void
  type?: string
  disabled?: boolean
}) {
  const id = `observe-${name.toLowerCase().replaceAll(" ", "-")}`
  return (
    <div className="min-w-0">
      <Label htmlFor={id}>{name}</Label>
      <Input
        id={id}
        type={type}
        value={value}
        disabled={disabled}
        onChange={(event) => change(event.target.value)}
      />
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
function HostReceipt({ receipt }: { receipt: HostFeedStatus | undefined }) {
  const observation = useContext(ObservationContext)
  const projection = useMemo(() => diagnosticProjection(receipt), [receipt])
  if (!receipt) return <p>No host-feed receipt reported</p>
  if (!observation) return null
  const counters = receipt.counters
  const lost = [
    counters?.overflow,
    counters?.lifecycle_overflow,
    counters?.unavailable,
    counters?.delivery_failed,
  ].some((value) => typeof value === "number" && value > 0)
  return (
    <section aria-label="Host-feed receipt" className="space-y-2">
      <p>Delivery counters are the last received report and may lag host accounting.</p>
      <details className="min-w-0">
        <summary className="cursor-pointer">Host-feed receipt metadata</summary>
        <DiagnosticPanel
          label="Host-feed receipt"
          schema={diagnosticSchema}
          {...projection}
          observation={observation}
        />
      </details>
      {lost && (
        <p className="text-status-failed">
          Retained telemetry may be incomplete: records were dropped or delivery failed.
        </p>
      )}
      <p className="text-text-muted">
        Queue-wait timeouts count attempts, not record loss. Counters reset with the host epoch and
        are not provider activity or complete history. Accepted records may be visible even when
        their acknowledgment was lost. No durable replay or exactly-once guarantee.
      </p>
    </section>
  )
}
function StatusView({ status }: { status: StatusSummary }) {
  const observation = useContext(ObservationContext)
  if (!observation || !status || typeof status !== "object" || Array.isArray(status))
    return <p role="alert">Invalid Observe status snapshot.</p>
  const dependencies = Array.isArray(status.dependencies) ? status.dependencies : []
  return (
    <div className="space-y-3 text-sm">
      <HealthSummary
        label="Observe reported aggregate"
        status={reportedHealth(status.health_status)}
        checks={[]}
        observation={observation}
      />
      <StatCollection label="Reported scalar gauges" rows={statusStats(status, observation)} />
      <p className="text-text-muted">
        Dependency reachability is not workload health. Active session status does not mean
        executing.
      </p>
      <HostReceipt receipt={status.host_feed} />
      <h3 className="font-medium">Dependency reachability</h3>
      {status.dependencies !== undefined && !Array.isArray(status.dependencies) && (
        <p role="alert">Invalid dependency reachability snapshot.</p>
      )}
      {dependencies.length ? (
        <ul className="space-y-2">
          {keyedSnapshot(dependencies).map(({ row: dependency, key }) => (
            <li key={key} className="min-w-0 break-words">
              <DependencyReachability dependency={dependency} observation={observation} />
              {dependency?.error && (
                <p className="text-status-failed">
                  The dependency reachability check reported an error.
                </p>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p>No dependency reachability reported</p>
      )}
    </div>
  )
}
export function DependencyReachability({
  dependency,
  observation,
}: {
  dependency: DependencyStatus
  observation: ObservationState
}) {
  const probe = reachability(dependency?.status)
  return (
    <ObservationStatus
      label={`${typeof dependency?.source === "string" ? dependency.source : "Unknown source"} reachability check`}
      observation={{
        ...observation,
        observedAt: typeof dependency?.checked_at === "string" ? dependency.checked_at : "",
      }}
    >
      <Pill tone={probe.tone}>{probe.label}</Pill>
    </ObservationStatus>
  )
}
export function ObserveStatus() {
  const verbs = useVerbs()
  const load = useCallback(() => observeApi.status().then(observeData).then(statusSnapshot), [])
  const read = useObserveRead(verbs.available === true && verbs.has("observe_status"), load)
  const nowMs = useObservationClock()
  if (verbs.loading || verbs.available !== true || !verbs.has("observe_status"))
    return (
      <ObservationStatus
        label="Observe status"
        observation={capabilityObservation(
          verbs.loading,
          verbs.available,
          verbs.has("observe_status"),
          nowMs,
        )}
      />
    )
  return (
    <section aria-label="Observe status" className="space-y-3 rounded border border-border p-4">
      <h2 className="text-lg font-medium">Observe status</h2>
      <ReadControls read={read} />
      <ReadRegion
        read={read}
        label="Observe status source time"
        observedAt={
          read.data
            ? typeof read.data.last_updated === "string"
              ? read.data.last_updated
              : ""
            : undefined
        }
      >
        {read.data ? <StatusView status={read.data} /> : <p>Status unavailable</p>}
      </ReadRegion>
    </section>
  )
}

export function MetricSample({
  at,
  label,
  children,
}: {
  at: string
  label: string
  children: ReactNode
}) {
  const observation = useContext(ObservationContext)
  if (!observation) return null
  return (
    <ObservationStatus
      label={label}
      observation={{ ...observation, observedAt: typeof at === "string" ? at : "" }}
    >
      {children}
    </ObservationStatus>
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
