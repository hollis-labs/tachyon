import { useState } from 'react'
import { TeamOperations } from '../agents/TeamOperations'
import { Btn } from '../agents/ui'
import { PageHeader } from '@hollis-labs/kit-dashboard'
import { ApiProvider } from '@tachyon/torque-client'
import { consoleClient } from '@console/client'
import { FleetAgentsPanel } from './fleet-agents'
import './fleet.css'

export { FleetAgentsPanel, FleetAgentsView } from './fleet-agents'
export { FleetAgentModal } from './fleet-agent-modal'
export { ActivityBars, PixelRobot, useFlip } from './agent-activity'
export { useFleetAgents } from './hooks/use-fleet-agents'

/**
 * Torque's /planner/agents (PlannerPage with db=agents) as a standalone page:
 * same page shell and content area, minus the planner search box and tab strip.
 */
export function FleetAgentsPage() {
  const [tab, setTab] = useState<'activity' | 'teams'>('activity')
  return (
    <ApiProvider client={consoleClient}>
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Agents" />
        <p className="border-b border-border px-4 py-2 dash-support text-fg-muted">Live agents, processes, and activity.</p>
        <nav aria-label="Agent views" className="flex gap-2 border-b border-border px-4 py-2"><Btn onClick={() => setTab('activity')} aria-pressed={tab === 'activity'}>Activity</Btn><Btn onClick={() => setTab('teams')} aria-pressed={tab === 'teams'}>Team operations</Btn></nav>
        <div className="flex-1 overflow-auto">
          {tab === 'activity' ? <FleetAgentsPanel /> : <TeamOperations />}
        </div>
      </div>
    </ApiProvider>
  )
}
