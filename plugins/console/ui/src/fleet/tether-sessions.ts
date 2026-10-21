import { useEffect, useState } from 'react'
import { dataRefreshRegistry } from '@tachyon/host-sdk'
import { runningSessions, type TetherSession } from './launch-client'
import type { FleetAgent } from './lib/fleet-agents'

/** Runtime session state supplies presence; only the activity monitor supplies working/idle state. */
export function mergeTetherSessions(agents: FleetAgent[], sessions: TetherSession[]): FleetAgent[] {
  const result = agents.map(a => ({ ...a }))
  const workspaceCounts = new Map<string, number>()
  for (const session of sessions) {
    if (session.state === 'running' && session.workspace) workspaceCounts.set(session.workspace, (workspaceCounts.get(session.workspace) ?? 0) + 1)
  }
  for (const session of sessions) {
    if (session.state !== 'running') continue
    const candidates = result.filter(a => a.sessionId === session.id || a.chat?.session?.id === session.id ||
      (!a.chat && workspaceCounts.get(session.workspace) === 1 && a.cwd === session.workspace) || a.cwd.endsWith(`/.tether-worker/workspaces/${session.id}/workroot`))
    if (candidates.length === 1) continue
    result.push({
      id: `tether:${session.id}`, sessionId: session.id, displayName: session.launch_id,
      provider: session.provider_id, pid: 0, cwd: session.workspace?.startsWith('tether-ref:') ? '' : session.workspace || '', state: 'unknown',
      groupLabel: `Tether · ${session.project_id || 'sessions'}`,
      sessionCreatedAt: Date.parse(session.created_at) || undefined,
      waitingFor: null, startedAt: null, lastActivity: null,
      stateConfidence: null, confidence: {}, model: null, messages: null,
      lastAction: { text: 'Tether reports running · activity details unavailable', at: null },
      workstream: session.workstream_id ? { id: session.workstream_id, name: null } : null,
    })
  }
  return result
}

export function useTetherSessions() {
  const [sessions, setSessions] = useState<TetherSession[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    let running = false
    let again = false
    const sync = async () => {
      if (running) { again = true; return }
      running = true
      try {
        const next = await runningSessions()
        if (active) { setSessions(next); setError(null) }
      } catch (e) { if (active) setError(e instanceof Error ? e.message : 'Tether sessions unavailable') }
      finally {
        running = false
        if (active && again) { again = false; void sync() }
      }
    }
    void sync()
    const off = dataRefreshRegistry('activity').subscribe(() => void sync())
    const timer = setInterval(() => void sync(), 5000)
    return () => { active = false; off(); clearInterval(timer) }
  }, [])
  return { sessions, error }
}
