import { Button, Dialog, DialogContent, DialogTitle, DialogDescription, Textarea } from '@hollis-labs/design-components'
import { Chip } from '../../components/chip'
import { InitialsAvatar } from '../../components/initials-avatar'
import { useEffect, useMemo, useState } from 'react'
import { useAgentsClient } from '../../agents/context'
import type { AgentRecord } from '../../agents/types'
import { useConsoleSeams } from '../../events/hooks'
import type { PendingPrSummary } from '../types'
import { assignAction } from './actions'
import {
  ASSIGN_TASKS,
  suggestTask,
  type AssignmentRecord,
  type AssignTask,
} from './assign'

/** Roles that fit the task best sort first. */
function fit(a: AgentRecord, task: AssignTask): number {
  const roles = a.identity.roles
  const want = task === 'review' ? ['reviewer'] : ['worker', 'implementer']
  return want.some((r) => roles.includes(r)) ? 0 : 1
}

export interface AssignDialogProps {
  pr: PendingPrSummary
  existing: AssignmentRecord[]
  /** Further PRs assigned in the same step (bulk assign). */
  also?: { pr: PendingPrSummary; existing: AssignmentRecord[] }[]
  /** Preselect a task instead of the one suggested from the PR's reasons. */
  initialTask?: AssignTask
  onClose: () => void
  onDone?: (message: string) => void
}

/** Agent picker + task + note. Dispatches `pr.assign` through the ActionBus (IdentityConfirm runs there). */
export function AssignDialog({ pr, existing, also = [], initialTask, onClose, onDone }: AssignDialogProps) {
  const targets = [{ pr, existing }, ...also]
  const agents = useAgentsClient()
  const { actionBus } = useConsoleSeams()
  const [list, setList] = useState<AgentRecord[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [task, setTask] = useState<AssignTask>(() => initialTask ?? suggestTask(pr))
  const [agentId, setAgentId] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const setForge = true
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
    return (list ?? [])
      .sort((a, b) => fit(a, task) - fit(b, task) || a.identity.name.localeCompare(b.identity.name))
  }, [list, task])

  const agent = list?.find((a) => a.identity.id === agentId) ?? null
  const dupOf = (e: AssignmentRecord[]) => (agent ? e.find((r) => r.agentId === agent.identity.id && r.task === task) : undefined)
  const dupCount = targets.filter((t) => dupOf(t.existing)).length

  const submit = async () => {
    if (!agent) return
    setBusy(true)
    setError(null)
    let ok = 0
    let last: string | undefined
    for (const t of targets) {
      const res = await actionBus.dispatch(
        assignAction({ pr: t.pr, agent: agent.identity, task, note, setForgeAssignee: setForge, force: !!dupOf(t.existing) }),
      )
      if (res.status === 'ok') ok++
      else last = res.status === 'denied' ? `Not confirmed: ${res.message ?? 'identity check failed'}` : (res.message ?? 'Assignment failed')
    }
    setBusy(false)
    if (ok === targets.length) {
      onDone?.(targets.length === 1 ? 'Assigned' : `Assigned ${ok} pull requests`)
      onClose()
    } else setError(targets.length === 1 ? (last ?? 'Assignment failed') : `${ok} of ${targets.length} assigned, ${targets.length - ok} failed: ${last}`)
  }

  const title = targets.length === 1 ? `${pr.ref.repo.name}#${pr.ref.number}` : `${targets.length} pull requests`
  const suggested = suggestTask(pr)
  const hint = (t: AssignTask) => {
    if (t !== suggested) return ''
    if (t === 'fix_checks') return `Suggested: ${pr.checksSummary?.failed ?? 'some'} ${pr.checksSummary?.failed === 1 ? 'check' : 'checks'} failing`
    if (t === 'resolve_conflicts') return 'Suggested: conflicts with the base branch'
    if (t === 'address_comments') return 'Suggested: changes requested'
    return pr.reasons.includes('review_requested') ? 'Suggested: review requested' : ''
  }
  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent data-dashboard-shape="square" aria-label={`Assign ${title} to an agent`} className="flex max-h-[90vh] flex-col gap-0 overflow-y-auto p-5 sm:max-w-xl">
        <DialogTitle className="font-sans text-base font-semibold normal-case tracking-normal text-fg">Assign to an agent</DialogTitle>
        <DialogDescription className="mt-2 flex flex-wrap items-center gap-2 dash-support text-fg-muted">
          <span className="min-w-0 truncate"><span className="font-mono">{title}</span>{targets.length === 1 && <> · {pr.title}</>}</span>
          {targets.length > 1 && <Chip>{`+ ${targets.length - 1} more selected`}</Chip>}
        </DialogDescription>

        <fieldset className="mt-4">
          <legend className="mb-2 font-mono dash-caption uppercase tracking-label text-fg-muted">Task</legend>
          <div role="radiogroup" aria-label="Task" className="space-y-1.5">
            {ASSIGN_TASKS.map((t) => (
              <label key={t.id} className={`flex cursor-pointer items-center gap-3 rounded border border-border px-3 py-2.5 dash-control ${task === t.id ? 'bg-surface-hover' : 'hover:bg-surface/40'}`}>
                <input type="radio" name="pr-task" value={t.id} checked={task === t.id} onChange={() => setTask(t.id)} className="shrink-0 accent-primary" />
                <span className="font-medium text-fg">{t.label}</span>
                <span className="ml-auto text-right dash-support text-fg-muted">{hint(t.id)}</span>
              </label>
            ))}
          </div>
        </fieldset>

        <fieldset className="mt-4">
          <legend className="mb-2 font-mono dash-caption uppercase tracking-label text-fg-muted">Agent</legend>
          {loadError && <p role="alert" className="dash-control text-danger">{loadError}</p>}
          {!list && !loadError && <p className="dash-control text-fg-faint">Loading agents…</p>}
          {list && shown.length === 0 && <p className="dash-control text-fg-faint">No enabled agents.</p>}
          <div role="radiogroup" aria-label="Agents" className="max-h-52 space-y-1.5 overflow-y-auto">
            {shown.map((a) => {
              const on = a.identity.id === agentId
              const done = existing.some((r) => r.agentId === a.identity.id && r.task === task)
              return (
                <label key={a.identity.id} className={`flex cursor-pointer items-center gap-3 rounded border border-border px-3 py-2.5 dash-control ${on ? 'bg-surface-hover' : 'hover:bg-surface/40'}`}>
                  <input type="radio" name="pr-assignee" value={a.identity.id} checked={on} onChange={() => setAgentId(a.identity.id)} className="shrink-0 accent-primary" />
                  <InitialsAvatar name={a.identity.name} />
                  <span className="min-w-0 truncate font-medium text-fg">{a.identity.name}</span>
                  {done && <Chip>assigned</Chip>}
                  <span className="ml-auto shrink-0 text-right dash-support text-fg-muted">{[a.identity.roles.join(', '), a.live?.state ?? 'not running'].filter(Boolean).join(' · ')}</span>
                </label>
              )
            })}
          </div>
          {agent && !agent.identity.urn && <p className="mt-1.5 dash-support text-fg-muted">No registered address for this agent; the message goes to a fallback address and may not be delivered.</p>}
        </fieldset>

        <Textarea aria-label="Note for the agent" value={note} onChange={(e) => setNote(e.target.value)} rows={3} className="mt-4 w-full resize-y dash-control" placeholder="Optional note for the agent…" />
        <p className="mt-3 dash-support text-fg-muted">Sends a mail with the PR link, why it is pending and what to do. The agent works on the PR branch and never merges.</p>

        {dupCount > 0 && <p role="status" className="mt-3 rounded border border-border bg-surface px-3 py-2 dash-control text-fg">{dupCount === 1 && targets.length === 1 ? `Already assigned to ${agent?.identity.name} for this task.` : `${dupCount} already assigned to ${agent?.identity.name} for this task.`} Sending again repeats the message.</p>}
        {error && <p role="alert" className="mt-3 rounded border border-danger/50 px-3 py-2 dash-control text-danger">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" type="button" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button type="button" onClick={submit} disabled={!agent || busy}>
            {busy ? 'Assigning…' : dupCount > 0 && targets.length === 1 ? 'Assign again' : targets.length > 1 ? `Assign ${targets.length}` : 'Assign'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
