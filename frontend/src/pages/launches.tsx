import { Button, Input, Label } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useCallback, useEffect, useMemo, useState } from "react"
import { PendingApprovalError } from "../api/hitl"
import { type AskDetail, invokeVerb, useVerbs } from "../api/verbs"
import { LargeDialog } from "../components/agent-ops/large-dialog"
import { PendingApproval } from "../components/pending-approval"

type LaunchState = "prepared" | "executing" | "running" | "completed" | "failed" | "cancelled"
export interface Launch {
  id: string
  backend?: string
  agent_id: string
  agent_name?: string
  provider?: string
  model?: string
  project_id?: string
  state: LaunchState
  session_id?: string
  error?: string
  config?: { work_item_id?: string }
  created_at: string
  updated_at: string
}
export interface LaunchStatus {
  launch_id: string
  state: LaunchState
  session_id?: string
  error?: string
  updated_at: string
}
interface AgentOption {
  id: string
  name: string
  status?: string
  can_execute?: boolean
}
const stateLabels: Record<LaunchState, string> = {
  prepared: "Prepared",
  executing: "Creating session",
  running: "Session created",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
}
const selectClass = "h-9 rounded-md border border-border-strong bg-bg px-3 text-sm"
const cancellable = (launch: Launch) => ["prepared", "executing", "running"].includes(launch.state)
const cancelLabel = (launch: Launch) =>
  launch.state === "executing"
    ? "Resolve interrupted launch"
    : launch.state === "prepared"
      ? "Cancel prepared launch"
      : launch.backend === "nanite"
        ? "Archive and request stop"
        : "Stop session"
const message = (error: unknown) => (error instanceof Error ? error.message : "Request failed")

async function read<T>(verb: string, payload: unknown = {}): Promise<T> {
  const result = await invokeVerb<T>(verb, payload)
  if (result.status === "error") throw new Error(result.error.message)
  if (result.status === "ask") throw new PendingApprovalError(result.ask)
  return result.data
}

export function LaunchesPage() {
  const verbs = useVerbs()
  const [launches, setLaunches] = useState<Launch[]>([])
  const [agents, setAgents] = useState<AgentOption[]>([])
  const [agentsLoading, setAgentsLoading] = useState(false)
  const [agentError, setAgentError] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  const [agentFilter, setAgentFilter] = useState("")
  const [stateFilter, setStateFilter] = useState("")
  const [from, setFrom] = useState("")
  const [until, setUntil] = useState("")
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<Launch | null>(null)
  const [detailError, setDetailError] = useState("")
  const [busy, setBusy] = useState(false)
  const [wizardOpen, setWizardOpen] = useState(false)
  const [step, setStep] = useState(1)
  const [agentId, setAgentId] = useState("")
  const [provider, setProvider] = useState("")
  const [model, setModel] = useState("")
  const [projectId, setProjectId] = useState("")
  const [workId, setWorkId] = useState("")
  const [wizardError, setWizardError] = useState("")
  const [prepared, setPrepared] = useState<Launch | null>(null)
  const [cancelOpen, setCancelOpen] = useState(false)
  const [reason, setReason] = useState("")
  const [listPending, setListPending] = useState<AskDetail | null>(null)
  const [agentPending, setAgentPending] = useState<AskDetail | null>(null)
  const [wizardPending, setWizardPending] = useState<AskDetail | null>(null)
  const [detailPending, setDetailPending] = useState<AskDetail | null>(null)

  const canList = verbs.has("launch_list")
  const canRead = verbs.has("launch_read")
  const canPoll = verbs.has("launch_status")
  const canChooseAgent = verbs.has("agent_list")
  const canPrepare = verbs.has("launch_prepare") && canChooseAgent
  const canExecute = verbs.has("launch_execute")
  const canCancel = verbs.has("launch_cancel")

  // biome-ignore lint/correctness/useExhaustiveDependencies: opening the wizard explicitly refreshes agent choices.
  useEffect(() => {
    if (!canChooseAgent) return
    let active = true
    setAgentsLoading(true)
    setAgentError("")
    setAgentPending(null)
    read<AgentOption[] | null>("agent_list")
      .then((items) => {
        if (active) setAgents(items ?? [])
      })
      .catch((failure) => {
        if (active) {
          if (failure instanceof PendingApprovalError) setAgentPending(failure.ask)
          else setAgentError(message(failure))
        }
      })
      .finally(() => {
        if (active) setAgentsLoading(false)
      })
    return () => {
      active = false
    }
  }, [canChooseAgent, wizardOpen])

  // Poll without overlapping requests on the serial plugin pipe.
  // biome-ignore lint/correctness/useExhaustiveDependencies: revision explicitly restarts polling after mutations or Refresh.
  useEffect(() => {
    if (!canList) return
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    setLoading(true)
    setListPending(null)
    let waiting = false
    async function refresh() {
      try {
        const items = await read<Launch[] | null>("launch_list")
        if (active) {
          setLaunches(items ?? [])
          setError("")
        }
      } catch (failure) {
        if (active) {
          if (failure instanceof PendingApprovalError) {
            waiting = true
            setListPending(failure.ask)
          } else setError(message(failure))
        }
      } finally {
        if (active) {
          setLoading(false)
          if (!waiting) timer = setTimeout(refresh, 5000)
        }
      }
    }
    void refresh()
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [canList, revision])

  // biome-ignore lint/correctness/useExhaustiveDependencies: revision invalidates in-flight detail responses after a mutation.
  useEffect(() => {
    if (!selectedId || !canRead) {
      setDetail(null)
      setDetailPending(null)
      return
    }
    if (detailPending) return
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    setDetail(null)
    setDetailError("")
    let waiting = false
    async function poll() {
      try {
        const status = await read<LaunchStatus>("launch_status", { launch_id: selectedId })
        if (active) {
          setDetail((current) =>
            current?.id === selectedId
              ? {
                  ...current,
                  state: status.state,
                  session_id: status.session_id,
                  error: status.error,
                  updated_at: status.updated_at,
                }
              : current,
          )
          setDetailError("")
        }
      } catch (failure) {
        if (active) {
          if (failure instanceof PendingApprovalError) {
            waiting = true
            setDetailPending(failure.ask)
          } else setDetailError(message(failure))
        }
      } finally {
        if (active && canPoll && !waiting) timer = setTimeout(poll, 3000)
      }
    }
    read<Launch>("launch_read", { launch_id: selectedId })
      .then((launch) => {
        if (!active) return
        setDetail(launch)
        if (canPoll) timer = setTimeout(poll, 3000)
      })
      .catch((failure) => {
        if (active) {
          if (failure instanceof PendingApprovalError) {
            waiting = true
            setDetailPending(failure.ask)
          } else setDetailError(message(failure))
        }
      })
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [selectedId, canRead, canPoll, revision, detailPending])

  const filtered = useMemo(
    () =>
      launches.filter((launch) => {
        if (agentFilter && launch.agent_id !== agentFilter) return false
        if (stateFilter && launch.state !== stateFilter) return false
        const created = Date.parse(launch.created_at)
        if (from && created < new Date(from).getTime()) return false
        if (until && created > new Date(until).getTime()) return false
        return true
      }),
    [launches, agentFilter, stateFilter, from, until],
  )

  const agentChoices = useMemo(() => {
    const choices = new Map(agents.map((agent) => [agent.id, agent.name]))
    for (const launch of launches)
      if (!choices.has(launch.agent_id))
        choices.set(launch.agent_id, launch.agent_name || launch.agent_id)
    return Array.from(choices.entries())
  }, [agents, launches])

  const refresh = useCallback(() => setRevision((current) => current + 1), [])

  function startWizard() {
    setAgentId("")
    setProvider("")
    setModel("")
    setProjectId("")
    setWorkId("")
    setPrepared(null)
    setWizardError("")
    setWizardPending(null)
    setStep(1)
    setWizardOpen(true)
  }

  async function prepare() {
    if (!canPrepare || !agentId || busy || wizardPending || agentPending) return
    setBusy(true)
    setWizardError("")
    try {
      const launch = await read<Launch>("launch_prepare", {
        agent_id: agentId,
        provider: provider.trim(),
        model: model.trim(),
        project_id: projectId.trim(),
        ...(workId.trim() ? { config: { work_item_id: workId.trim() } } : {}),
      })
      setPrepared(launch)
      setStep(3)
      refresh()
    } catch (failure) {
      if (failure instanceof PendingApprovalError) setWizardPending(failure.ask)
      else setWizardError(message(failure))
    } finally {
      setBusy(false)
    }
  }

  async function execute(launch: Launch, wizard = false) {
    if (
      !canExecute ||
      launch.state !== "prepared" ||
      busy ||
      (wizard ? wizardPending : detailPending)
    )
      return
    setBusy(true)
    if (wizard) setWizardError("")
    else setDetailError("")
    try {
      const result = await read<Launch>("launch_execute", { launch_id: launch.id })
      if (wizard) setWizardOpen(false)
      setSelectedId(result.id)
      refresh()
    } catch (failure) {
      if (failure instanceof PendingApprovalError) {
        if (wizard) setWizardPending(failure.ask)
        else setDetailPending(failure.ask)
      } else if (wizard) setWizardError(message(failure))
      else setDetailError(message(failure))
    } finally {
      setBusy(false)
    }
  }

  async function cancel() {
    if (!detail || !canCancel || !cancellable(detail) || busy || detailPending) return
    setBusy(true)
    setDetailError("")
    try {
      await read<Launch>("launch_cancel", { launch_id: detail.id, reason: reason.trim() })
      setCancelOpen(false)
      refresh()
    } catch (failure) {
      if (failure instanceof PendingApprovalError) setDetailPending(failure.ask)
      else setDetailError(message(failure))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Launches" />
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border-strong p-4">
        <p className="text-sm text-text-muted">
          Prepare an agent launch, review it, then create a session.
        </p>
        {canPrepare && <Button onClick={startWizard}>New launch</Button>}
      </div>
      {listPending && (
        <div className="px-4">
          <PendingApproval ask={listPending} />
        </div>
      )}
      {verbs.loading ? (
        <p className="p-4">Loading capabilities…</p>
      ) : !canList ? (
        <p className="p-4" role="status">
          Launches are unavailable from the current provider.
        </p>
      ) : (
        <>
          <div className="flex flex-wrap items-end gap-3 p-4">
            <div className="grid gap-1">
              <Label htmlFor="launch-agent-filter">Agent</Label>
              <select
                id="launch-agent-filter"
                className={selectClass}
                value={agentFilter}
                onChange={(event) => setAgentFilter(event.target.value)}
              >
                <option value="">All agents</option>
                {agentChoices.map(([id, name]) => (
                  <option key={id} value={id}>
                    {name}
                  </option>
                ))}
              </select>
            </div>
            <div className="grid gap-1">
              <Label htmlFor="launch-state-filter">Status</Label>
              <select
                id="launch-state-filter"
                className={selectClass}
                value={stateFilter}
                onChange={(event) => setStateFilter(event.target.value)}
              >
                <option value="">All statuses</option>
                {Object.entries(stateLabels).map(([state, label]) => (
                  <option key={state} value={state}>
                    {label}
                  </option>
                ))}
              </select>
            </div>
            <div className="grid gap-1">
              <Label htmlFor="launch-from">Created from</Label>
              <Input
                id="launch-from"
                type="datetime-local"
                value={from}
                onChange={(event) => setFrom(event.target.value)}
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor="launch-until">Created until</Label>
              <Input
                id="launch-until"
                type="datetime-local"
                value={until}
                onChange={(event) => setUntil(event.target.value)}
              />
            </div>
            <Button
              variant="outline"
              onClick={() => {
                setAgentFilter("")
                setStateFilter("")
                setFrom("")
                setUntil("")
              }}
            >
              Clear filters
            </Button>
            <Button variant="outline" onClick={refresh} disabled={loading}>
              Refresh
            </Button>
          </div>
          {error && (
            <p className="px-4 pb-3 text-danger" role="alert">
              {error}
            </p>
          )}
          {from && until && from > until && (
            <p className="px-4 pb-3" role="alert">
              The end of the range must follow the start.
            </p>
          )}
          <div className="overflow-auto p-4 pt-0">
            {loading && launches.length === 0 ? (
              <p>Loading launches…</p>
            ) : filtered.length === 0 ? (
              <p role="status">
                {launches.length
                  ? "No launches match these filters."
                  : "No launches yet. Prepare your first launch to get started."}
              </p>
            ) : (
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border-strong">
                    <th className="p-3">Agent</th>
                    <th className="p-3">Status</th>
                    <th className="p-3">Provider / model</th>
                    <th className="p-3">Created</th>
                    <th className="p-3">Launch</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((launch) => (
                    <tr key={launch.id} className="border-b border-border-strong">
                      <td className="p-3">{launch.agent_name || launch.agent_id}</td>
                      <td className="p-3">{stateLabels[launch.state] ?? launch.state}</td>
                      <td className="p-3">
                        {launch.provider || "Default"} / {launch.model || "Default"}
                      </td>
                      <td className="p-3">{new Date(launch.created_at).toLocaleString()}</td>
                      <td className="p-3">
                        {canRead ? (
                          <Button
                            variant="outline"
                            onClick={() => {
                              setSelectedId(launch.id)
                              setCancelOpen(false)
                            }}
                          >
                            View {launch.id}
                          </Button>
                        ) : (
                          launch.id
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}
      <LargeDialog
        open={wizardOpen}
        onClose={() => {
          if (!busy) setWizardOpen(false)
        }}
        title={`New launch · Step ${step} of 3`}
        description={
          step === 3
            ? "Review the prepared launch before creating a session."
            : "Select an agent and configure its session."
        }
        footer={
          <>
            <Button variant="outline" disabled={busy} onClick={() => setWizardOpen(false)}>
              {prepared ? "Keep prepared and close" : "Close"}
            </Button>
            {step === 2 && (
              <Button variant="outline" disabled={busy} onClick={() => setStep(1)}>
                Back
              </Button>
            )}
            {step === 1 && (
              <Button
                disabled={
                  !agentId ||
                  busy ||
                  agentsLoading ||
                  !!agentError ||
                  !!agentPending ||
                  !!wizardPending
                }
                onClick={() => setStep(2)}
              >
                Configure
              </Button>
            )}
            {step === 2 && (
              <Button
                aria-disabled={!canPrepare || busy || !!wizardPending}
                onClick={() => void prepare()}
              >
                {busy ? "Preparing…" : "Prepare and review"}
              </Button>
            )}
            {step === 3 && prepared && canExecute && (
              <Button
                aria-disabled={busy || !!wizardPending}
                onClick={() => void execute(prepared, true)}
              >
                {busy ? "Creating session…" : "Create session"}
              </Button>
            )}
          </>
        }
      >
        {wizardPending && <PendingApproval ask={wizardPending} />}
        {agentPending && <PendingApproval ask={agentPending} />}
        {wizardError && (
          <p role="alert" className="mb-3 text-danger">
            {wizardError}
          </p>
        )}
        {step === 1 && (
          <div className="grid gap-2">
            <Label htmlFor="launch-agent">Agent</Label>
            <select
              id="launch-agent"
              className={selectClass}
              value={agentId}
              onChange={(event) => setAgentId(event.target.value)}
            >
              <option value="">Select an agent</option>
              {agents
                .filter((agent) => agent.status !== "disabled" && agent.can_execute !== false)
                .map((agent) => (
                  <option key={agent.id} value={agent.id}>
                    {agent.name}
                  </option>
                ))}
            </select>
            {agentError && (
              <p role="alert" className="text-danger">
                {agentError}
              </p>
            )}
            {agentsLoading && <p role="status">Loading agents…</p>}
            {!agentsLoading &&
              !agentError &&
              !agentPending &&
              !agents.some(
                (agent) => agent.status !== "disabled" && agent.can_execute !== false,
              ) && (
                <p className="text-sm">
                  No executable agents available. Check Agent Ops, then reopen this wizard.
                </p>
              )}
          </div>
        )}
        {step === 2 && (
          <div className="grid max-w-xl gap-4">
            <div className="grid gap-1">
              <Label htmlFor="launch-provider">Provider (optional)</Label>
              <Input
                id="launch-provider"
                value={provider}
                onChange={(event) => setProvider(event.target.value)}
                placeholder="Use the configured default"
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor="launch-model">Model (optional)</Label>
              <Input
                id="launch-model"
                value={model}
                onChange={(event) => setModel(event.target.value)}
                placeholder="Use the configured default"
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor="launch-project">Project ID (optional)</Label>
              <Input
                id="launch-project"
                value={projectId}
                onChange={(event) => setProjectId(event.target.value)}
              />
            </div>
            <div className="grid gap-1">
              <Label htmlFor="launch-work">Work item ID (optional)</Label>
              <Input
                id="launch-work"
                value={workId}
                onChange={(event) => setWorkId(event.target.value)}
              />
              <p className="text-xs text-text-muted">Saved as a reference on this launch.</p>
            </div>
          </div>
        )}
        {step === 3 && prepared && <LaunchDetails launch={prepared} />}
      </LargeDialog>
      <LargeDialog
        open={selectedId !== null}
        onClose={() => {
          if (!busy) {
            setSelectedId(null)
            setCancelOpen(false)
          }
        }}
        title="Launch details"
        description={canPoll ? "Status refreshes every three seconds." : "Current launch details."}
        footer={
          <>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => {
                setSelectedId(null)
                setCancelOpen(false)
              }}
            >
              Close
            </Button>
            {detail?.state === "prepared" && canExecute && (
              <Button aria-disabled={busy || !!detailPending} onClick={() => void execute(detail)}>
                Create session
              </Button>
            )}
            {detail && cancellable(detail) && canCancel && (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => {
                  setReason("")
                  setCancelOpen(true)
                }}
              >
                {cancelLabel(detail)}
              </Button>
            )}
          </>
        }
      >
        {detailPending && <PendingApproval ask={detailPending} />}
        {detailError && (
          <p role="alert" className="mb-3 text-danger">
            {detailError}
          </p>
        )}
        {detail ? (
          <LaunchDetails launch={detail} />
        ) : (
          !detailError && !detailPending && <p>Loading launch…</p>
        )}
        {detail?.state === "executing" && (
          <p className="mt-3">
            Active session creation must finish before cancellation. An interrupted launch can be
            resolved; without a session ID, its provider outcome remains unknown.
          </p>
        )}
        {cancelOpen && (
          <div className="mt-5 grid max-w-xl gap-3 border-t border-border-strong pt-4">
            <p>
              {detail?.state === "prepared"
                ? "Cancel this prepared launch? No session will be created."
                : detail?.state === "executing" && !detail.session_id
                  ? "Resolve this interrupted launch as cancelled? A provider session may exist; no stop can be requested without its ID. Active creation will reject this request."
                  : detail?.backend === "nanite"
                    ? "Archive this Nanite session and request runtime shutdown? Nanite retains its conversation; it does not report whether shutdown succeeded."
                    : "Stop this provider session? Cancellation is recorded after the provider accepts the stop request."}
            </p>
            <Label htmlFor="launch-cancel-reason">Reason (optional)</Label>
            <Input
              id="launch-cancel-reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
            <div className="flex gap-2">
              <Button aria-disabled={busy || !!detailPending} onClick={() => void cancel()}>
                {busy ? "Applying…" : detail ? cancelLabel(detail) : "Confirm"}
              </Button>
              <Button variant="outline" disabled={busy} onClick={() => setCancelOpen(false)}>
                {detail?.state === "prepared" ? "Keep launch" : "Keep session"}
              </Button>
            </div>
          </div>
        )}
      </LargeDialog>
    </div>
  )
}

function LaunchDetails({ launch }: { launch: Launch }) {
  const rows = [
    ["Launch", launch.id],
    ["Agent", launch.agent_name || launch.agent_id],
    ["Status", stateLabels[launch.state] ?? launch.state],
    ["Provider", launch.provider || "Configured default"],
    ["Model", launch.model || "Configured default"],
    ["Project", launch.project_id || "None"],
    ["Work item", launch.config?.work_item_id || "None"],
    ["Session", launch.session_id || "Not created"],
    ["Created", new Date(launch.created_at).toLocaleString()],
    ["Updated", new Date(launch.updated_at).toLocaleString()],
  ]
  return (
    <>
      <dl className="grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-text-muted">{label}</dt>
            <dd className="break-all">{value}</dd>
          </div>
        ))}
      </dl>
      {launch.error && (
        <p role="alert" className="mt-4 text-danger">
          {launch.error}
        </p>
      )}
    </>
  )
}
