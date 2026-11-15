import { consoleClient } from '@console/client'
import { ApiProvider } from '@tachyon/torque-client'
import { TeamBuilder } from '../agents/TeamBuilder'
import { BuilderShell } from './BuilderShell'

/** Team definitions, kept in Builder until a composition module takes them. */
export function BuilderTeamsPage() {
  return (
    <ApiProvider client={consoleClient}>
      <div className="flex h-full min-w-0 flex-col">
        <BuilderShell tab="teams" />
        <div className="flex-1 overflow-auto">
          <TeamBuilder />
        </div>
      </div>
    </ApiProvider>
  )
}
