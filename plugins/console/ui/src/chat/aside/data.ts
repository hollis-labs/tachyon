import { useCallback, useEffect, useRef, useState } from 'react'
import { chatJSON } from '../roster'
import { useThreadFrames, type Frame } from '../live'
import { DEMO } from '../cards/useBlocks'
import { DEMO_ACTIVITY, DEMO_BOOKMARKS, DEMO_PLAN, DEMO_RULES, DEMO_TOPICS, DEMO_WORK } from './fixtures'

const enc = encodeURIComponent
const JSON_HEADERS = { 'Content-Type': 'application/json' }
const msg = (e: unknown, fallback: string) => (e instanceof Error ? e.message : fallback)

export interface PlanStep { id: string; position: number; title: string; status: 'todo' | 'doing' | 'blocked' | 'done' | 'cancelled'; task_ref?: string | null }
export interface Plan { id: string; title?: string | null; status: string; revision: number; task_ref?: string | null; steps: PlanStep[] }
export interface Topic { id: string; title: string; message_ref: string; seq?: number | null; added_by: 'user' | 'agent'; created_at: number }
export interface Bookmark { id: string; message_ref: string; note: string; created_at: number }
export interface WorkItem { id: string; kind: 'theme' | 'task' | 'decision'; title: string; status: string; progress?: number | null; needs_user?: boolean; updated_at: number }
export interface Activity { seq: number; type: string; actor: string; cite?: { kind: string; ref: string; label: string }[]; payload?: { title?: string; summary?: string; body?: string; review_ref?: string; cite?: { label: string }[] } | null; at?: number }
export interface Rule { id: string; tool: string; effect: string; created_at: number }

/** Server frames for a thread (one shared stream); `panel` frames drive the auto aside, the rest are prompts to refetch. */
export function useThreadEvents(ref: string | undefined, onFrame: (f: Frame) => void) {
  useThreadFrames(ref, onFrame, !DEMO)
}

/** Fetch once, refetch on a tick and when `signal` changes. A failed read is an error string, never a crash. */
function useRead<T>(ref: string | undefined, path: string, pick: (d: never) => T, empty: T, demo: T | undefined, signal: number) {
  const [data, setData] = useState<T>(demo ?? empty)
  const [loading, setLoading] = useState(!!ref && demo === undefined)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    if (demo !== undefined) { setData(demo); setLoading(false); return }
    if (!ref) { setData(empty); setLoading(false); return }
    try { setData(pick(await chatJSON<never>(`/threads/${enc(ref)}${path}`))); setError('') }
    catch (e) {
      // 404 on plan means "no plan yet", not a failure.
      if (e instanceof Error && /No such plan|not_found|404/.test(e.message)) { setData(empty); setError('') } else setError(msg(e, 'Could not load.'))
    } finally { setLoading(false) }
    // biome-ignore lint/correctness/useExhaustiveDependencies: pick and empty are stable by construction
  }, [ref, path, demo])
  useEffect(() => { void load() }, [load, signal])
  return { data, loading, error, reload: load, set: setData }
}

export function usePlan(ref: string | undefined, signal: number) {
  const r = useRead<Plan | null>(ref, '/plan', (d: Plan) => d, null, DEMO ? DEMO_PLAN : undefined, signal)
  const [err, setErr] = useState('')
  const setStatus = async (s: PlanStep, status: PlanStep['status']) => {
    const plan = r.data
    if (!plan) return
    r.set({ ...plan, steps: plan.steps.map((x) => (x.id === s.id ? { ...x, status } : x)) })
    if (DEMO || !ref) return
    try { await chatJSON(`/threads/${enc(ref)}/plan/steps/${enc(s.id)}`, { method: 'PATCH', headers: JSON_HEADERS, body: JSON.stringify({ status, expected_revision: plan.revision }) }); setErr(''); await r.reload() }
    catch (e) { setErr(msg(e, 'Could not update the step.')); await r.reload() }
  }
  const addStep = async (title: string) => {
    if (DEMO) { const p = r.data; if (p) r.set({ ...p, steps: [...p.steps, { id: `n${p.steps.length}`, position: p.steps.length, title, status: 'todo' }] }); return }
    if (!ref) return
    try {
      if (!r.data) await chatJSON(`/threads/${enc(ref)}/plan`, { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify({ scope: 'thread', title: null, steps: [{ title }] }) })
      else await chatJSON(`/threads/${enc(ref)}/plan/steps`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ title }) })
      setErr(''); await r.reload()
    } catch (e) { setErr(msg(e, 'Could not add the step.')) }
  }
  return { plan: r.data, loading: r.loading, error: err || r.error, setStatus, addStep }
}

export function useTopics(ref: string | undefined, signal: number) {
  const r = useRead<Topic[]>(ref, '/topics', (d: { topics: Topic[] }) => d.topics ?? [], [], DEMO ? DEMO_TOPICS : undefined, signal)
  const add = async (title: string, messageRef: string) => {
    if (DEMO || !ref) return
    try { await chatJSON(`/threads/${enc(ref)}/topics`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ title, message_ref: messageRef }) }); await r.reload() } catch { /* shown on next read */ }
  }
  return { topics: r.data, loading: r.loading, error: r.error, add }
}

export function useBookmarks(ref: string | undefined, signal: number) {
  const r = useRead<Bookmark[]>(ref, '/bookmarks', (d: { bookmarks: Bookmark[] }) => d.bookmarks ?? [], [], DEMO ? DEMO_BOOKMARKS : undefined, signal)
  const toggle = async (messageRef: string) => {
    const have = r.data.find((b) => b.message_ref === messageRef)
    if (DEMO) { r.set(have ? r.data.filter((b) => b !== have) : [...r.data, { id: messageRef, message_ref: messageRef, note: '', created_at: Date.now() }]); return }
    if (!ref) return
    try {
      if (have) await chatJSON(`/threads/${enc(ref)}/bookmarks/${enc(have.id)}`, { method: 'DELETE' })
      else await chatJSON(`/threads/${enc(ref)}/bookmarks`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ message_ref: messageRef }) })
      await r.reload()
    } catch { /* the read after reflects the server */ }
  }
  return { bookmarks: r.data, loading: r.loading, error: r.error, toggle }
}

export function useWork(ref: string | undefined, agent: string | undefined, signal: number) {
  const r = useRead<{ items: WorkItem[]; source?: string; assignee_resolved?: boolean }>(ref, `/work${agent ? `?agent=${enc(agent)}` : ''}`, (d: { items: WorkItem[]; source?: string; assignee_resolved?: boolean }) => d, { items: [] }, DEMO ? DEMO_WORK : undefined, signal)
  return { items: r.data.items ?? [], source: r.data.source, loading: r.loading, error: r.error }
}

export function useActivity(ref: string | undefined, signal: number) {
  const r = useRead<{ activity: Activity[]; has_more?: boolean }>(ref, '/activity?limit=50', (d: { activity: Activity[]; has_more?: boolean }) => d, { activity: [] }, DEMO ? DEMO_ACTIVITY : undefined, signal)
  const [err, setErr] = useState('')
  const [older, setOlder] = useState<{ rows: Activity[]; hasMore: boolean } | null>(null)
  useEffect(() => { setOlder(null) }, [ref])
  const first = r.data.activity ?? []
  const events = older ? [...first, ...older.rows.filter((o) => !first.some((f) => f.seq === o.seq))] : first
  const hasMore = older ? older.hasMore : !!r.data.has_more
  /** Older events: the next page before the oldest seq we hold. */
  const more = async () => {
    if (DEMO || !ref || !events.length) return
    const before = Math.min(...events.map((e) => e.seq))
    try {
      const d = await chatJSON<{ activity: Activity[]; has_more?: boolean }>(`/threads/${enc(ref)}/activity?limit=50&before_seq=${before}`)
      setOlder({ rows: [...(older?.rows ?? []), ...(d.activity ?? [])], hasMore: !!d.has_more }); setErr('')
    } catch (e) { setErr(msg(e, 'Could not load older activity.')) }
  }
  const comment = async (body: string) => {
    if (DEMO || !ref) return true
    try { await chatJSON(`/threads/${enc(ref)}/activity/comments`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ body }) }); setErr(''); await r.reload(); return true }
    catch (e) { setErr(msg(e, 'Could not post the comment. Your text is kept.')); return false }
  }
  return { events, hasMore, loading: r.loading, error: err || r.error, comment, more }
}

export function useRules(ref: string | undefined, signal: number) {
  const r = useRead<Rule[]>(ref, '/approval-rules', (d: { rules: Rule[] }) => d.rules ?? [], [], DEMO ? DEMO_RULES : undefined, signal)
  const revoke = async (id: string) => {
    if (DEMO) { r.set(r.data.filter((x) => x.id !== id)); return }
    if (!ref) return
    try { await chatJSON(`/threads/${enc(ref)}/approval-rules/${enc(id)}`, { method: 'DELETE' }); await r.reload() } catch { /* the read after reflects the server */ }
  }
  return { rules: r.data, revoke }
}
