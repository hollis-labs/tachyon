import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { approvalClient, type ApprovalResponse } from '../../approvals/client'
import { chatJSON } from '../roster'
import { useThreadFrames } from '../live'
import { demoBlocks } from './fixtures'
import type { Commit } from './Cards'
import type { StreamBlock } from './types'

interface ApiCard {
  item_id: string
  kind: string
  caption?: string
  fields?: { id: string; type: string; label?: string; options?: { id?: string; value?: string; label?: string; description?: string; recommended?: boolean }[] }[]
  actions?: { id: string; label: string }[]
  target?: { ref?: string; op?: string } | null
  state: 'pending' | 'resolved' | 'expired' | 'superseded'
  class?: 'low' | 'medium'
  reversible?: boolean
  result?: { answers?: { value?: unknown }[]; decision?: string } | null
  created_at?: number
  message_ref?: string | null
}

/** Chat API Card projection -> stream block. Kinds the stream has no block for return null and stay in Approvals. */
export function cardToBlock(c: ApiCard): StreamBlock | null {
  const base = { id: c.item_id, itemId: c.item_id, at: c.created_at ?? 0, afterRef: c.message_ref ?? undefined }
  const open = c.state === 'pending'
  if (c.state === 'expired' || c.state === 'superseded') return null
  if (c.kind === 'card.approval' || c.kind === 'card.tool_approval') {
    return { ...base, kind: 'approval', title: c.caption ?? 'Approval', tool: c.target?.op ?? c.target?.ref ?? 'tool call', canAlways: c.class === 'low', reversible: c.reversible ?? false, answered: open ? undefined : { decision: c.result?.decision === 'denied' ? 'denied' : 'approved' } }
  }
  const f = c.fields?.find((x) => x.type === 'enum')
  if (c.kind === 'card.question' && f?.options?.length) {
    const options = f.options.map((o, i) => ({ id: o.id ?? o.value ?? String(i), label: o.label ?? o.value ?? o.id ?? '', description: o.description, recommended: o.recommended }))
    const chosen = c.result?.answers?.[0]?.value
    return { ...base, kind: 'decision', title: c.caption ?? f.label ?? 'Decision', options, reversible: c.reversible ?? true, answered: open ? undefined : { optionId: String(chosen ?? options[0].id) } }
  }
  return null
}

/**
 * The blocks between messages for one thread. Live cards come from GET threads/{ref}/cards (read-only here; a failed
 * read is empty, not an error). `?demo=cards` swaps in the golden r2 fixtures so the design is reviewable on the real route.
 */
// Read once at load: the chat route redirects to /chat/<thread> and drops the query string.
export const DEMO = typeof location !== 'undefined' && new URLSearchParams(location.search).get('demo') === 'cards'

interface ApiRun { run_id: string; machine?: string; machine_id?: string; command: string; state: string; requested_by?: string; approval?: { mode?: string }; exit_code?: number | null; output?: string; truncated?: boolean; close_on_complete?: boolean; closed_at?: number | null; started_at?: number | null; ended_at?: number | null; duration_ms?: number | null }
interface ApiPlan { id: string; title?: string | null; updated_at?: number; created_at?: number; steps: { id: string; title: string; status: string; position: number }[] }

/** Run record -> run card. pending_approval / denied / expired live on the proposal, not the card. */
export function runToBlock(r: ApiRun): StreamBlock | null {
  const state = r.state === 'running' || r.state === 'finished' || r.state === 'failed' || r.state === 'stopped' ? r.state : null
  if (!state) return null
  const started = r.started_at ?? 0
  const seconds = r.duration_ms != null ? Math.round(r.duration_ms / 100) / 10 : state === 'running' && started ? Math.max(0, Math.round((Date.now() - started) / 1000)) : 0
  return { id: `run-${r.run_id}`, kind: 'run', at: started, run: { runId: r.run_id, machine: r.machine ?? r.machine_id ?? 'host', requestedBy: r.requested_by ?? 'agent', approval: r.approval?.mode === 'policy' ? 'policy' : 'approved', command: r.command, state, exitCode: r.exit_code ?? undefined, seconds, output: r.output ?? '', closeOnComplete: !!r.close_on_complete, closed: !!r.closed_at, truncated: r.truncated } }
}

/** The thread plan as a stream card: progress over all steps, the first few steps shown (open ones first). */
export function planToBlock(p: ApiPlan): StreamBlock | null {
  const steps = p.steps.filter((s) => s.status !== 'cancelled').sort((a, b) => a.position - b.position)
  if (!steps.length) return null
  const done = steps.filter((s) => s.status === 'done').length
  const doing = steps.filter((s) => s.status === 'doing').length
  const state = (s: string) => (s === 'done' ? 'done' : s === 'doing' ? 'doing' : 'todo') as 'done' | 'doing' | 'todo'
  const shown = [...steps.filter((s) => s.status !== 'done').slice(0, 3), ...steps.filter((s) => s.status === 'done').slice(-1)].sort((a, b) => a.position - b.position)
  return { id: `plan-${p.id}`, kind: 'plan', at: p.updated_at ?? p.created_at ?? 0, title: p.title ?? 'Plan', total: steps.length, done, doing, steps: shown.map((s) => ({ id: s.id, title: s.title, state: state(s.status) })) }
}

export interface RunActions { stop: (runId: string) => Promise<void>; close: (runId: string) => Promise<void>; setCloseOnComplete: (runId: string, v: boolean) => Promise<void> }

export function useBlocks(ref: string | undefined) {
  const demo = DEMO
  const [cards, setCards] = useState<StreamBlock[]>([])
  const [plan, setPlan] = useState<StreamBlock | null>(null)
  const [runs, setRuns] = useState<StreamBlock[]>([])
  const [error, setError] = useState('')
  const enc = ref ? encodeURIComponent(ref) : ''
  const loadCards = useCallback(() => chatJSON<{ cards: ApiCard[] }>(`/threads/${enc}/cards`).then((d) => { setCards((d.cards ?? []).map(cardToBlock).filter((b): b is StreamBlock => !!b)); setError('') }, (e) => setError(e instanceof Error ? e.message : 'Could not load cards.')), [enc])
  const loadPlan = useCallback(() => chatJSON<ApiPlan>(`/threads/${enc}/plan`).then((p) => setPlan(planToBlock(p)), () => setPlan(null)), [enc])
  const loadRuns = useCallback(() => chatJSON<{ runs: ApiRun[] }>(`/terminal/runs?thread_ref=${enc}&closed=false`).then((d) => setRuns((d.runs ?? []).map(runToBlock).filter((b): b is StreamBlock => !!b)), () => setRuns([])), [enc])
  useEffect(() => {
    if (demo) { setCards(demoBlocks(Date.now())); return }
    setCards([]); setPlan(null); setRuns([])
    if (!ref) return
    void loadCards(); void loadPlan(); void loadRuns()
  }, [ref, demo, loadCards, loadPlan, loadRuns])
  // Frames are signals: refetch what the frame names. Run output arrives in bursts, so the run refetch is coalesced.
  const runTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useThreadFrames(ref, (f) => {
    if (f.type === 'card') void loadCards()
    else if (f.type === 'plan') void loadPlan()
    else if (f.type === 'terminal.run') { if (!runTimer.current) runTimer.current = setTimeout(() => { runTimer.current = null; void loadRuns() }, 400) }
    else if (f.type === 'reconnected') { void loadCards(); void loadPlan(); void loadRuns() }
  }, !demo)
  useEffect(() => () => { if (runTimer.current) clearTimeout(runTimer.current) }, [])

  /** Answers go through the Approvals queue, the only record of the decision; demo blocks have no item and stay local. */
  const commit: Commit = useCallback(async (block, value) => {
    if (!block.itemId) return
    const item = await approvalClient.get(block.itemId, true)
    const response = toResponse(block, value, item.request_snapshot.kind)
    await approvalClient.decide(item, response)
    void loadCards()
  }, [loadCards])
  const runActions = useMemo<RunActions>(() => ({
    stop: async (id) => { await chatJSON(`/terminal/runs/${encodeURIComponent(id)}/stop`, { method: 'POST' }); void loadRuns() },
    close: async (id) => { await chatJSON(`/terminal/runs/${encodeURIComponent(id)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ closed: true }) }); void loadRuns() },
    setCloseOnComplete: async (id, v) => { await chatJSON(`/terminal/runs/${encodeURIComponent(id)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ close_on_complete: v }) }) },
  }), [loadRuns])
  const blocks = useMemo(() => (demo ? cards : [...cards, ...(plan ? [plan] : []), ...runs]), [demo, cards, plan, runs])
  return { blocks, commit, runActions: demo ? undefined : runActions, error }
}

function toResponse(block: StreamBlock, value: unknown, kind: string): ApprovalResponse {
  if (block.kind === 'decision') {
    const v = value as { optionId: string; note?: string }
    return { kind: kind as ApprovalResponse['kind'], decision: 'submitted', note: v.note, answers: [{ questionId: 'choice', value: v.optionId }] }
  }
  const d = value as 'approved' | 'always' | 'denied'
  return { kind: kind as ApprovalResponse['kind'], decision: d === 'denied' ? 'denied' : 'approved', note: d === 'always' ? 'always' : undefined }
}
