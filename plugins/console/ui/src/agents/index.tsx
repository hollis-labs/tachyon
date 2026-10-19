import { PageHeader } from '@hollis-labs/kit-dashboard'
import { ApiProvider } from '@tachyon/torque-client'
import { consoleClient } from '@console/client'
import { AgentDirectoryView } from './AgentDirectory'
import { AdminTabs } from '@hollis-labs/kit-admin'
import { useSearchParams } from 'react-router-dom'
import { ProjectsView, WorkstreamsView } from '../objects/ObjectsView'
import { OwnedBuilderView } from './OwnedBuilderView'
import { TeamBuilder } from './TeamBuilder'
import { useState } from 'react'

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
  const [visited, setVisited] = useState(() => new Set([section]))
  return (
    <ApiProvider client={consoleClient}>
      <div data-dashboard-accent="yellow" className="flex h-full min-w-0 flex-col">
        <PageHeader title="Builder" />
        <AdminTabs label="Builder areas" value={section}
          onChange={key => { setVisited(previous => new Set([...previous, key])); setQuery(current => { const next = new URLSearchParams(current); next.set('section', key); return next }) }}
          items={[{ key: 'agents', label: 'Agents' }, { key: 'teams', label: 'Teams' }, { key: 'assignments', label: 'Assignments' }, { key: 'catalog', label: 'Provider catalog' }, { key: 'projects', label: 'Projects' }, { key: 'workstreams', label: 'Workstreams' }]} />
        <div className="flex-1 overflow-auto">
          {(visited.has('agents') || section === 'agents') && <div hidden={section !== 'agents'}><OwnedBuilderView kind="agent" /></div>}
          {(visited.has('assignments') || section === 'assignments') && <div hidden={section !== 'assignments'}><OwnedBuilderView kind="assignment" /></div>}
          {(visited.has('teams') || section === 'teams') && <div hidden={section !== 'teams'}><TeamBuilder /></div>}
          {section === 'projects' ? <ProjectsView /> : section === 'workstreams' ? <WorkstreamsView /> : section === 'catalog' ? <AgentDirectoryView /> : null}
        </div>
      </div>
    </ApiProvider>
  )
}
