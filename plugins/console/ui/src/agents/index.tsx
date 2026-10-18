import { PageHeader } from '@hollis-labs/kit-dashboard'
import { ApiProvider } from '@tachyon/torque-client'
import { consoleClient } from '@console/client'
import { AgentDirectoryView } from './AgentDirectory'
import '../fleet/fleet.css'
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
        <nav aria-label="Builder areas" className="flex flex-wrap gap-2 border-b border-zinc-800/80 px-4 py-2">
          {(['agents', 'projects', 'workstreams'] as const).map(key => <button key={key} type="button" aria-current={section === key ? 'page' : undefined} onClick={() => setQuery(current => { const next = new URLSearchParams(current); next.set('section', key); return next })} className={`px-3 py-2 font-mono text-xs uppercase tracking-wide focus-visible:ring-1 focus-visible:ring-amber-300 ${section === key ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-400 hover:text-zinc-200'}`}>{key === 'agents' ? 'Agents & teams' : key}</button>)}
        </nav>
        <div className="flex-1 overflow-auto">
          {section === 'projects' ? <ProjectsView /> : section === 'workstreams' ? <WorkstreamsView /> : <AgentDirectoryView />}
        </div>
      </div>
    </ApiProvider>
  )
}
