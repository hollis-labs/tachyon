import type { FleetAgent } from '../fleet/lib/fleet-agents'
import type { ConsoleEvent } from '../events/types'

export const RECENT_MS = 90_000
export function toolLabel(tool: string): string {
  if (/message.*(send|notify|reply)/i.test(tool)) return 'Sending a message'
  if (/message.*(read|consume|get|inbox|list)/i.test(tool)) return 'Checking messages'
  if (/(^|[_.:/])(edit|write|write_file|apply_patch|multiedit)([_.:/]|$)/i.test(tool)) return 'Editing a file'
  if (/(^|[_.:/])(read|read_file)([_.:/]|$)/i.test(tool)) return 'Reading a file'
  if (/(search|grep|glob|recall)/i.test(tool)) return 'Searching'
  if (/(exec_command|bash|shell|run_command)/i.test(tool)) return 'Running a command'
  return `Using ${tool.replace(/^mcp__[^_]+__/, '').replace(/_/g, ' ')}`
}
export function activityText(e: ConsoleEvent): string | null {
  const p = e.payload
  if (e.kind.startsWith('tool.call.')) {
    const tool = typeof p.tool === 'string' ? p.tool : ''
    return tool ? `${toolLabel(tool)}${e.kind.endsWith('finished') ? p.ok === false ? ' · failed' : ' · finished' : ''}` : null
  }
  if (e.kind === 'progress.checkpoint') return typeof p.label === 'string' ? p.label.slice(0, 200) : 'Reported progress'
  if (e.kind === 'session.turn_output') return p.kind === 'final' ? 'Finished a response' : 'Produced output'
  if (e.kind === 'session.turn_routed') return 'Response sent'
  if (e.kind === 'session.launch_failed') return 'Session failed to start'
  if (e.kind === 'session.state_changed') return typeof p.to === 'string' ? `Session ${p.to}` : 'Session state changed'
  return null
}
export function latestActivity(events: ConsoleEvent[], sessionId?: string): ConsoleEvent | undefined {
  return sessionId ? events.filter(e => e.sessionId === sessionId && activityText(e)).sort((a,b) => b.ts - a.ts || Number(b.cursor) - Number(a.cursor))[0] : undefined
}

export interface SessionHealth { alive?: boolean; live_state?: string; last_activity_at?: string; pending_approval?: boolean; pending_question?: boolean }
export interface Presence { text: string; tone: 'active' | 'waiting' | 'quiet'; at?: number; detail: string }
export function presence(health: SessionHealth | null, event: ConsoleEvent | undefined, now: number, checkedAt: number, error: boolean): Presence {
  const fresh = !error && checkedAt > 0 && now - checkedAt < 20_000
  const at = Math.max(event?.ts || 0, Date.parse(health?.last_activity_at || '') || 0) || undefined
  if (!fresh) return { text: at ? 'Last seen' : 'Presence unavailable', at, tone: 'quiet', detail: 'No fresh observation. Polling resumes when the connection is available.' }
  if (health?.alive === false) return { text: 'Not running', at, tone: 'quiet', detail: 'Tether reports this runtime is not alive.' }
  if (health?.pending_approval || health?.pending_question) return { text: health.pending_approval ? 'Waiting for approval' : 'Waiting for an answer', at, tone: 'waiting', detail: 'Reported by the runtime.' }
  const idle = health?.live_state === 'idle'
  if (event && now - event.ts < RECENT_MS && !idle) {
    return { text: activityText(event)!, at: event.ts, tone: event.kind === 'tool.call.started' ? 'active' : 'quiet', detail: 'Latest observed activity; polled every 5 seconds. This may already have finished.' }
  }
  if (health?.alive) return { text: idle ? 'Online · idle' : ['busy','running','working','processing'].includes(health.live_state || '') ? 'Working' : 'Online', at, tone: idle ? 'quiet' : 'active', detail: 'Runtime observed online; this is a polled snapshot.' }
  return { text: at ? 'Last seen' : 'No activity observed', at, tone: 'quiet', detail: 'No current runtime health is available.' }
}

/** Exact identity only: a provider-native id may differ from the managed session id. */
export function monitorForSession(agents: FleetAgent[], session: string) {
  const matches = agents.filter(a => a.sessionId === session || a.id === session || a.cwd.endsWith(`/.tether-worker/workspaces/${session}/workroot`))
  return matches.length === 1 ? matches[0] : undefined
}

