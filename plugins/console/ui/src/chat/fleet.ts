import type { FleetAgent } from '../fleet/lib/fleet-agents'
import type { ChatAgent } from './roster'

/** Match session identity, or a unique Tether-managed workspace; never display names. */
export function mergeChatAgents(processes: FleetAgent[], roster: ChatAgent[]): FleetAgent[] {
  const result = processes.map((a) => ({ ...a }))
  for (const chat of roster) {
    // The activity source reports the provider-native session id, while Tether
    // reports its runtime session id. Its managed workspace contains the latter.
    const candidates = chat.session
      ? result.filter(
          (a) =>
            a.sessionId === chat.session?.id ||
            a.id === chat.session?.id ||
            a.cwd.endsWith(`/.tether-worker/workspaces/${chat.session?.id}/workroot`),
        )
      : []
    const found = candidates.length === 1 ? candidates[0] : undefined
    if (found) {
      found.chat = chat
      found.displayName = chat.display_name
      continue
    }
    const running = chat.session?.state === 'running'
    result.push({
      id: `chat:${chat.launch_id}`,
      provider: 'tether',
      displayName: chat.display_name,
      chat,
      pid: 0,
      sessionId: chat.session?.id ?? null,
      cwd: '',
      state: running ? (chat.session?.at_turn_boundary ? 'waiting' : 'busy') : 'unknown',
      waitingFor: running && chat.session?.at_turn_boundary ? 'Ready for your next message' : null,
      startedAt: null,
      lastActivity: chat.session ? Date.parse(chat.session.last_activity_at) || null : null,
      stateConfidence: 'exact',
      confidence: { state: 'exact' },
      model: null,
      lastAction: {
        text: running
          ? chat.session?.at_turn_boundary
            ? 'Ready for your next message'
            : 'Working on a turn'
          : `Ready to start · last session ${chat.session?.state ?? 'not started'}`,
        at: null,
      },
      workstream: null,
      messages: null,
    })
  }
  return result
}
