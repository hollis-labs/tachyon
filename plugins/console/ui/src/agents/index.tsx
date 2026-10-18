import { PageHeader } from '@hollis-labs/kit-dashboard'
import { ApiProvider } from '@tachyon/torque-client'
import { consoleClient } from '@console/client'
import { AgentDirectoryView } from './AgentDirectory'
import { AdminTabs } from '@hollis-labs/kit-admin'
import { useSearchParams } from 'react-router-dom'
import { ProjectsView, WorkstreamsView } from '../objects/ObjectsView'

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

/** Configuration workspace; execution and current process state belong to Agents. */
export function BuilderPage() {
  const [query, setQuery] = useSearchParams()
  const section = query.get('section') ?? 'agents'
  return (
    <ApiProvider client={consoleClient}>
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Builder" />
        <AdminTabs label="Builder areas" value={section}
          onChange={key => setQuery(current => { const next = new URLSearchParams(current); next.set('section', key); return next })}
          items={[{ key: 'agents', label: 'Agents & teams' }, { key: 'projects', label: 'Projects' }, { key: 'workstreams', label: 'Workstreams' }]} />
        <div className="flex-1 overflow-auto">
          {section === 'projects' ? <ProjectsView /> : section === 'workstreams' ? <WorkstreamsView /> : <AgentDirectoryView />}
        </div>
      </div>
    </ApiProvider>
  )
}
