import { PageHeader } from '@hollis-labs/kit-dashboard'
import { ApiProvider } from '@tachyon/torque-client'
import { consoleClient } from '@console/client'
import { AgentDirectoryView } from './AgentDirectory'
import '../fleet/fleet.css'

export { AgentDirectoryView, AgentDetail } from './AgentDirectory'
export { AgentsClientProvider, useAgentsClient } from './context'
export { createHttpAgentsClient, AgentsHttpError, AGENTS_API_BASE } from './http-client'
export type { AgentsClient } from './client'
export * from './types'
export { AgentEditor } from './AgentEditor'
export { AuditTrail } from './AuditTrail'
export { PlanDiff, PlanReview } from './PlanReview'
export { StepUpHost } from './StepUpDialog'
export { AgentEditProvider, resolveStepUp, useAgentEdit } from './edit-context'
export { AGENT_ACTIONS, agentPlanAction, registerAgentActions } from './actions'
export * from './plan'

/**
 * Torque's /planner/builder (PlannerPage with db=builder) as a standalone page:
 * same page shell and content area, minus the planner search box and tab strip.
 * The directory talks to the real HTTP AgentsClient (see http-client.ts).
 */
export function BuilderPage() {
  return (
    <ApiProvider client={consoleClient}>
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Builder" />
        <div className="flex-1 overflow-auto">
          <AgentDirectoryView />
        </div>
      </div>
    </ApiProvider>
  )
}
