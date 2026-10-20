import { useEffect, useState } from 'react'
import { consoleClient } from '../client'
import { parseFleetAgents, type FleetAgent } from '../fleet/lib/fleet-agents'
import { ago } from '../lib/agent-activity'
import { latestActivity, monitorForSession, presence, RECENT_MS, type SessionHealth } from './model'
import { observationJSON, useObservations } from './store'

interface Observation { session: string; health: SessionHealth | null; monitor?: FleetAgent; checkedAt: number; error: boolean }

export function AgentPresence({ sessionId, address }: { sessionId?: string; address?: string }) {
  const events = useObservations()
  const [observed, setObserved] = useState<Observation | null>(null)
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    let active = true
    let running = false
    setObserved(null)
    const tick = async () => {
      setNow(Date.now())
      if (running || document.visibilityState === 'hidden') return
      running = true
      let session = sessionId || (/^msg:\/\/session\/[^/]+\/([^/]+)$/.exec(address || '')?.[1] ?? '')
      try {
        if (!session && address?.startsWith('msg://agent/')) {
          const binding = await observationJSON<{ SessionID?: string; LeaseExpiresAt?: string }>('/binding?urn=' + encodeURIComponent(address))
          if (binding.LeaseExpiresAt && Date.parse(binding.LeaseExpiresAt) <= Date.now()) throw new Error('Binding expired')
          session = binding.SessionID || ''
        }
        if (!session) throw new Error('No session binding')
        const [health, monitor] = await Promise.allSettled([
          observationJSON<SessionHealth>(`/sessions/${encodeURIComponent(session)}/health`),
          consoleClient.getActivityAgents(),
        ])
        const local = monitor.status === 'fulfilled' ? monitorForSession(parseFleetAgents(monitor.value), session) : undefined
        if (active) setObserved(prev => health.status === 'rejected' && !local && prev?.session === session
          ? { ...prev, error: true }
          : { session, health: health.status === 'fulfilled' ? health.value : null,
            monitor: local, checkedAt: Date.now(), error: health.status === 'rejected' && !local })
      } catch {
        if (active) setObserved(prev => prev && prev.session === session ? { ...prev, error: true } : { session, health: null, checkedAt: 0, error: true })
      } finally { running = false }
    }
    void tick()
    const timer = setInterval(() => void tick(), 5000)
    const focus = () => void tick()
    window.addEventListener('focus', focus)
    document.addEventListener('visibilitychange', focus)
    return () => { active = false; clearInterval(timer); window.removeEventListener('focus', focus); document.removeEventListener('visibilitychange', focus) }
  }, [sessionId, address])
  const event = latestActivity(events.events, observed?.session || sessionId)
  const freshEvents = !events.error && now - events.checkedAt < 20_000
  const state = presence(observed?.health ?? null, freshEvents ? event : undefined, now, observed?.checkedAt ?? events.checkedAt, observed ? observed.error : !!events.error)
  const local = observed?.monitor
  const localAt = local?.lastAction?.at
  const freshLocal = observed && !observed.error && now - observed.checkedAt < 20_000 && localAt && now - localAt < RECENT_MS && localAt > (event?.ts ?? 0)
  const label = freshLocal ? local?.lastAction?.text : state.text
  const at = freshLocal ? localAt : state.at
  const tone = freshLocal ? local?.state === 'busy' ? 'active' : local?.state === 'waiting' ? 'waiting' : 'quiet' : state.tone
  return <div aria-label="Agent activity" className="flex min-w-0 items-center gap-1.5 font-mono text-caption text-fg-muted" title={freshLocal ? 'Latest activity reported by the activity monitor; polled every 5 seconds.' : state.detail}>
    <span aria-hidden className={`size-1.5 shrink-0 rounded-full ${tone === 'active' ? 'bg-success motion-safe:animate-pulse' : tone === 'waiting' ? 'bg-warning' : 'bg-fg-faint'}`} />
    <span className="truncate">{label}</span>
    {at && <time className="shrink-0 text-fg-faint" dateTime={new Date(at).toISOString()} title={new Date(at).toLocaleString()}>· {ago(at, now)}</time>}
  </div>
}
