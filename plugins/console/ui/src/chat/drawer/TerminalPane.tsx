import { useEffect, useState } from 'react'
import { Chip } from '@planner/shared'
import { Act } from '../cards/Block'
import { TERMINAL_BASE } from '../roster'
import { useThreadFrames } from '../live'
import { operatorFetch } from '../../operator/elevation'
import { CAPTION } from '@planner/shared'

/** terminal/* needs operator assurance even for reads: opening the terminal is the moment the operator is asked. */
async function tj<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await operatorFetch(`${TERMINAL_BASE}${path}`, { ...init, headers: { Accept: 'application/json', ...init?.headers } })
  const body = await res.json().catch(() => null)
  if (!res.ok) throw Object.assign(new Error(body?.message ?? body?.error?.message ?? `Terminal request failed (${res.status})`), { status: res.status })
  return body as T
}

interface Machine { machine_id: string; label: string; reachable?: boolean | null }
interface Proposal { id: string; command: string; reason?: string; requested_by: string; state: string; op_digest?: string }

/** The golden's quick terminal, shown as fixtures on ?demo=cards. */
const DEMO_MACHINES: Machine[] = [{ machine_id: 'dev-box', label: 'dev-box', reachable: true }]
const DEMO_PROPOSAL: Proposal = { id: 'p1', command: 'pnpm test --filter chat', requested_by: 'Portfolio Manager', state: 'pending' }

/**
 * Quick terminal: machine picker, connection line, agent-proposed commands (Run / Edit / Deny through the HITL policy).
 * Backend T8 is not live on every host: a 404 or 501 reads as one visible line, never as an error page.
 */
export function TerminalPane({ threadRef, demo }: { threadRef: string | undefined; demo?: boolean }) {
  const [machines, setMachines] = useState<Machine[] | null>(demo ? DEMO_MACHINES : null)
  const [unavailable, setUnavailable] = useState('')
  const [machine, setMachine] = useState(demo ? 'dev-box' : '')
  const [proposals, setProposals] = useState<Proposal[]>(demo ? [DEMO_PROPOSAL] : [])
  const [error, setError] = useState('')
  useEffect(() => {
    if (demo) return
    let live = true
    tj<{ machines?: Machine[] }>('/machines').then((d) => {
      if (!live) return
      setMachines(d.machines ?? []); setMachine(d.machines?.[0]?.machine_id ?? '')
    }, (e: Error & { status?: number }) => {
      if (!live) return
      setUnavailable(e.status === 404 || e.status === 501 ? 'The terminal is not available on this host yet. Commands the agent proposes will show here once it is.' : e.status === 401 || e.status === 403 ? 'The terminal needs an unlocked operator session. Unlock, then reopen the terminal.' : 'Could not reach the terminal service. Try again in a moment.')
    })
    return () => { live = false }
  }, [demo])
  const [reload, setReload] = useState(0)
  useThreadFrames(threadRef, (f) => { if (f.type === 'terminal.run' || f.type === 'card' || f.type === 'reconnected') setReload((n) => n + 1) }, !demo)
  useEffect(() => {
    if (demo || !threadRef || !machines) return
    tj<{ proposals: Proposal[] }>(`/proposals?thread_ref=${encodeURIComponent(threadRef)}&state=pending`).then((d) => setProposals(d.proposals ?? []), () => undefined)
  }, [demo, threadRef, machines, reload])

  const act = async (p: Proposal, verb: 'run' | 'deny') => {
    setError('')
    if (demo) { setProposals((xs) => xs.filter((x) => x.id !== p.id)); return }
    try {
      await tj(`/proposals/${encodeURIComponent(p.id)}/${verb}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(verb === 'run' ? { op_digest: p.op_digest } : {}) })
      setProposals((xs) => xs.filter((x) => x.id !== p.id))
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not answer the proposal. It is still waiting.') }
  }

  if (unavailable) return <p role="status" className="text-xs text-fg-muted">{unavailable}</p>
  if (!machines) return <p role="status" className="text-xs text-fg-muted">Connecting…</p>
  if (!machines.length) return <p role="status" className="text-xs text-fg-muted">No machines are reachable from this host. Add one in Settings, then reopen the terminal.</p>
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <label className="inline-flex items-center gap-1.5">
          <span className="sr-only">Machine</span>
          <select value={machine} onChange={(e) => setMachine(e.target.value)} className="h-5 rounded-full border border-border bg-transparent px-2 text-label text-fg-secondary">
            {machines.map((m) => <option key={m.machine_id} value={m.machine_id}>● {m.label}</option>)}
          </select>
        </label>
        <span className="text-label text-fg-muted">{demo ? 'ssh · saved key · connected' : 'ssh · saved key'}</span>
        <span className="flex-1" />
        <Chip square>+ Connect to…</Chip>
      </div>
      <div role="log" aria-label="Terminal output" className="flex-1 whitespace-pre-wrap rounded-control border border-border-subtle bg-bg-elevated px-2.5 py-2 font-mono text-xs leading-relaxed text-fg-secondary">
        {demo ? <><span className="text-fg-muted">chris@dev-box ~/dev/hollis-labs</span> $ git status -sb{'\n'}<span className="text-fg">## main...origin/main</span>{'\n'} M apps/tachyon/plugins/chat/ui/src/Chat.tsx{'\n'}<span className="text-fg-muted">chris@dev-box ~/dev/hollis-labs</span> $ </> : <span className="text-fg-muted">Session output appears here.</span>}
        {proposals.map((p) => (
          <div key={p.id} className="mt-2 flex flex-wrap items-center gap-2 border-t border-border-subtle pt-2 font-sans">
            <span className={CAPTION}>{p.requested_by} wants to run</span>
            <code className="rounded-sm bg-surface px-1.5 py-0.5 font-mono text-xs text-fg">{p.command}</code>
            <Act primary kbd="↵" onClick={() => void act(p, 'run')}>Run</Act>
            <Act disabled title="Edit lands with the live terminal">Edit</Act>
            <Act danger onClick={() => void act(p, 'deny')}>Deny</Act>
            <span className="text-label text-fg-muted">needs approval · policy</span>
          </div>
        ))}
      </div>
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
    </>
  )
}
