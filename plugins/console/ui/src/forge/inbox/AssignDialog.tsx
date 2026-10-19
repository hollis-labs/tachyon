import { Button, Dialog, DialogContent, DialogTitle, DialogDescription, SearchInput, Textarea } from '@hollis-labs/design-components'
import { useEffect, useMemo, useState } from 'react'
import { useAgentsClient } from '../../agents/context'
import type { AgentRecord } from '../../agents/types'
import { useConsoleSeams } from '../../events/hooks'
import type { PendingPrSummary } from '../types'
import { assignAction } from './actions'
import {
  ASSIGN_TASKS,
  buildAssignmentPayload,
  renderAssignmentBody,
  suggestTask,
  type AssignmentRecord,
  type AssignTask,
} from './assign'

const LIVE_DOT = { waiting: 'bg-warning', busy: 'bg-success', idle: 'bg-info', offline: 'bg-fg-faint' } as const

/** Roles that fit the task best sort first. */
function fit(a: AgentRecord, task: AssignTask): number {
  const roles = a.identity.roles
  const want = task === 'review' ? ['reviewer'] : ['worker', 'implementer']
  return want.some((r) => roles.includes(r)) ? 0 : 1
}

export interface AssignDialogProps {
  pr: PendingPrSummary
  existing: AssignmentRecord[]
  onClose: () => void
  onDone?: (message: string) => void
}

/** Agent picker + task + note. Dispatches `pr.assign` through the ActionBus (IdentityConfirm runs there). */
export function AssignDialog({ pr, existing, onClose, onDone }: AssignDialogProps) {
  const agents = useAgentsClient()
  const { actionBus } = useConsoleSeams()
  const [list, setList] = useState<AgentRecord[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [task, setTask] = useState<AssignTask>(() => suggestTask(pr))
  const [agentId, setAgentId] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [setForge, setSetForge] = useState(true)
  const [preview, setPreview] = useState(false)
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    agents.listAgents().then(
      (r) => live && setList(r.filter((a) => a.identity.enabled)),
      (e) => live && setLoadError(e instanceof Error ? e.message : 'Could not load agents'),
    )
    return () => {
      live = false
    }
  }, [agents])


  const shown = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (list ?? [])
      .filter((a) => !q || `${a.identity.name} ${a.identity.id} ${a.identity.roles.join(' ')}`.toLowerCase().includes(q))
      .sort((a, b) => fit(a, task) - fit(b, task) || a.identity.name.localeCompare(b.identity.name))
  }, [list, search, task])

  const agent = list?.find((a) => a.identity.id === agentId) ?? null
  const dup = agent ? existing.find((r) => r.agentId === agent.identity.id && r.task === task) : undefined
  const body = useMemo(
    () => (agent ? renderAssignmentBody(buildAssignmentPayload(pr, task, agent.identity.id, { note })) : ''),
    [agent, pr, task, note],
  )

  const submit = async () => {
    if (!agent) return
    setBusy(true)
    setError(null)
    const res = await actionBus.dispatch(
      assignAction({ pr, agent: agent.identity, task, note, setForgeAssignee: setForge, force: !!dup }),
    )
    setBusy(false)
    if (res.status === 'ok') {
      onDone?.(res.message ?? 'Assigned')
      onClose()
    } else setError(res.status === 'denied' ? `Not confirmed: ${res.message ?? 'identity check failed'}` : (res.message ?? 'Assignment failed'))
  }

  const title = `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}`
  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent showCloseButton={false} data-dashboard-shape="square" data-dashboard-accent="yellow" aria-label={`Assign ${title} to an agent`} className="dash-detail-dialog flex max-h-[84vh] flex-col gap-0 p-0 sm:max-w-2xl">
        <header className="border-b border-border px-4 py-3">
          <DialogTitle className="font-mono dash-caption uppercase tracking-widest text-fg-muted">Assign to an agent</DialogTitle>
          <DialogDescription className="mt-1 truncate font-mono dash-body text-fg">{title} <span className="text-fg-muted">{pr.title}</span></DialogDescription>
        </header>

        <div className="space-y-4 overflow-y-auto px-4 py-4">
          <fieldset>
            <legend className="mb-1.5 font-mono dash-caption uppercase tracking-widest text-fg-faint">Task</legend>
            <div className="flex flex-wrap gap-1.5">
              {ASSIGN_TASKS.map((t) => (
                <Button variant="outline" size="sm" key={t.id} type="button" aria-pressed={task === t.id} onClick={() => setTask(t.id)} className={`font-mono dash-label uppercase tracking-widest ${task === t.id ? 'border-warning text-warning' : 'border-border text-fg-muted hover:bg-surface'}`}>
                  {t.label}
                </Button>
              ))}
            </div>
          </fieldset>

          <div>
            <div className="mb-1.5 flex items-center justify-between gap-3">
              <span className="font-mono dash-caption uppercase tracking-widest text-fg-faint">Agent</span>
              <div className="dash-search-adapter min-w-0 max-w-56"><SearchInput ariaLabel="Search agents" value={search} onChange={setSearch} placeholder="Search agents" debounceMs={0} slashToFocus={false} /></div>
            </div>
            {loadError && <p role="alert" className="font-mono dash-control text-danger">{loadError}</p>}
            {!list && !loadError && <p className="font-mono dash-control text-fg-faint">Loading agents…</p>}
            {list && shown.length === 0 && <p className="font-mono dash-control text-fg-faint">No matching enabled agents.</p>}
            <div role="radiogroup" aria-label="Agents" className="max-h-52 gap-0 overflow-y-auto border border-border">
              {shown.map((a) => {
                const on = a.identity.id === agentId
                const done = existing.some((r) => r.agentId === a.identity.id && r.task === task)
                return (
                  <label key={a.identity.id} className="block">
                    <div className={`dash-selectable-row flex w-full items-center gap-3 border-b border-border/80 px-3 py-2 text-left font-mono dash-control ${on ? 'bg-surface-hover text-fg' : 'text-fg-muted'}`}>
                      <input type="radio" name="pr-assignee" value={a.identity.id} checked={on} onChange={() => setAgentId(a.identity.id)} className="shrink-0 accent-primary" />
                      <span className={`h-1.5 w-1.5 shrink-0 ${LIVE_DOT[a.live?.state ?? 'offline']}`} aria-hidden />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate">{a.identity.name}</span>
                        <span className="block truncate dash-support text-fg-muted">{a.identity.roles.join(', ') || 'no role'} · {a.runtime.provider}{a.live ? ` · ${a.live.state}` : ' · not running'}</span>
                      </span>
                      {done && <span className="border border-success/50 px-1 dash-shortcut uppercase tracking-widest text-success">assigned</span>}
                    </div>
                  </label>
                )
              })}
            </div>
            {agent && !agent.identity.urn && <p className="mt-1.5 font-mono dash-support text-warning">No registered address for this agent; the message goes to a fallback address and may not be delivered.</p>}
          </div>

          <label className="block">
            <span className="mb-1.5 block font-mono dash-caption uppercase tracking-widest text-fg-faint">Note for the agent (optional)</span>
            <Textarea aria-label="Note for the agent" value={note} onChange={(e) => setNote(e.target.value)} rows={3} className="w-full resize-y dash-control" placeholder="Anything the PR context does not say" />
          </label>

          <label className="flex items-center gap-2 font-mono dash-control text-fg-muted">
            <input type="checkbox" checked={setForge} onChange={(e) => setSetForge(e.target.checked)} className="accent-primary" />
            Also set the agent as the PR assignee on the forge
          </label>

          {agent && (
            <div>
              <Button variant="outline" size="sm" type="button" onClick={() => setPreview((v) => !v)} aria-expanded={preview} className="font-mono dash-support uppercase tracking-widest text-fg-muted hover:text-fg">
                {preview ? '▾' : '▸'} Message preview
              </Button>
              {preview && <pre className="mt-1.5 max-h-56 overflow-auto whitespace-pre-wrap border border-border bg-surface/60 p-2 font-mono dash-support text-fg-muted">{body}</pre>}
            </div>
          )}

          {dup && <p role="status" className="border border-warning/50 bg-warning/5 px-3 py-2 font-mono dash-control text-warning">Already assigned to {agent?.identity.name} for this task. Sending again repeats the message.</p>}
          {error && <p role="alert" className="border border-danger/50 bg-danger/5 px-3 py-2 font-mono dash-control text-danger">{error}</p>}
        </div>

        <footer className="flex items-center justify-between gap-3 border-t border-border px-4 py-3">
          <span className="font-mono dash-caption text-fg-faint">Needs identity confirmation: {setForge ? 'forge assign + message' : 'message'}</span>
          <span className="flex gap-2">
            <Button variant="outline" size="sm" type="button" onClick={onClose} disabled={busy} className={`font-mono dash-label uppercase tracking-widest border-border text-fg-muted hover:bg-surface`}>Cancel</Button>
            <Button variant="outline" size="sm" type="button" onClick={submit} disabled={!agent || busy} className={`font-mono dash-label uppercase tracking-widest border-warning text-warning hover:bg-warning/10`}>
              {busy ? 'Assigning…' : dup ? 'Assign again' : 'Assign'}
            </Button>
          </span>
        </footer>
      </DialogContent>
    </Dialog>
  )
}
