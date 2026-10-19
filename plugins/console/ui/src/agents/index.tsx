import { PageHeader } from '@hollis-labs/kit-dashboard/ui'
import { ListPageLayout, TabStrip } from '@hollis-labs/kit-dashboard/layout'
import { ApiProvider } from '@tachyon/torque-client'
import { consoleClient } from '@console/client'
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

type Section = 'agents' | 'teams' | 'assignments' | 'projects' | 'workstreams'
const SECTIONS: readonly Section[] = ['agents', 'teams', 'assignments', 'projects', 'workstreams']

/** Configuration workspace; execution and current process state belong to Agents. */
export function BuilderPage() {
  const [query, setQuery] = useSearchParams()
  const requested = query.get('section')
  // The retired Provider catalog tab folds into Agents; old links keep working.
  const section: Section = SECTIONS.find(s => s === requested) ?? 'agents'
  const [visited, setVisited] = useState(() => new Set([section]))
  return (
    <ApiProvider client={consoleClient}>
      <div data-dashboard-accent="yellow" className="flex h-full min-w-0 flex-col">
        <ListPageLayout
          header={<PageHeader title="Builder" />}
          tabs={<TabStrip value={section} onChange={key => { setVisited(previous => new Set([...previous, key])); setQuery(current => { const next = new URLSearchParams(current); next.set('section', key); return next }) }}
            tabs={[{ key: 'agents', label: 'Agents' }, { key: 'teams', label: 'Teams' }, { key: 'assignments', label: 'Assignments' }, { key: 'projects', label: 'Projects' }, { key: 'workstreams', label: 'Workstreams' }]} />}>
          {(visited.has('agents') || section === 'agents') && <div hidden={section !== 'agents'}><OwnedBuilderView kind="agent" /></div>}
          {(visited.has('assignments') || section === 'assignments') && <div hidden={section !== 'assignments'}><OwnedBuilderView kind="assignment" /></div>}
          {(visited.has('teams') || section === 'teams') && <div hidden={section !== 'teams'}><TeamBuilder /></div>}
          {section === 'projects' ? <ProjectsView /> : section === 'workstreams' ? <WorkstreamsView /> : null}
        </ListPageLayout>
      </div>
    </ApiProvider>
  )
}
