import type { AgentState } from '@console/fleet/lib/agent-activity'
import type { FleetState } from '@console/fleet/lib/fleet-agents'

export const FLEET_STATE_STYLE: Record<FleetState, { label: string; dot: string; text: string; fill: string; avatar: AgentState }> = {
  waiting: { label: 'WAITING ON YOU', dot: 'bg-warning', text: 'text-warning', fill: 'var(--color-warning)', avatar: 'blocked' },
  busy: { label: 'WORKING', dot: 'bg-success', text: 'text-success', fill: 'var(--color-success)', avatar: 'working' },
  idle: { label: 'IDLE', dot: 'bg-info', text: 'text-info', fill: 'var(--color-info)', avatar: 'idle' },
  unknown: { label: 'UNKNOWN', dot: 'bg-fg-faint', text: 'text-fg-muted', fill: 'var(--color-fg-faint)', avatar: 'idle' },
}
