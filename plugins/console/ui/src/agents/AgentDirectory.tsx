import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { AdminRecordButton, AdminSection as Section, AdminFact, IdentityAvatar, ActivityStatus } from '@hollis-labs/kit-admin'
import { ago } from '@console/fleet/lib/agent-activity'
import { Chip } from './ui'
import { isPrivileged, policySummary } from './filter'
import type { AgentLiveRef, AgentRecord } from './types'

const LIVE_LABEL: Record<AgentLiveRef['state'], string> = { waiting: 'WAITING ON YOU', busy: 'WORKING', idle: 'IDLE', offline: 'OFFLINE' }

const FLEET_PATH = '/agents'

function LiveBadge({ live, unknown }: { live?: AgentLiveRef; unknown?: boolean }) {
  return <ActivityStatus label={live ? LIVE_LABEL[live.state] : unknown ? 'NOT CONNECTED' : 'NOT RUNNING'} tone={live?.state === 'busy' ? 'positive' : live?.state === 'waiting' ? 'attention' : live?.state === 'idle' ? 'neutral' : 'muted'} />
}

export function Row({ agent, selected, onSelect }: { agent: AgentRecord; selected: boolean; onSelect: () => void }) {
  const { identity, runtime } = agent
  return <li><AdminRecordButton selected={selected} onSelect={onSelect} muted={!identity.enabled} size="comfortable"
    avatar={<IdentityAvatar identity={identity.id} size="definition" />}
    title={<span className="flex flex-wrap items-center gap-x-3 gap-y-0.5">{identity.name}
      {identity.retired && <Chip tone="text-danger">retired</Chip>}
      {!identity.enabled && !identity.retired && <Chip>disabled</Chip>}
      {isPrivileged(agent) && <Chip tone="text-warning">privileged</Chip>}
    </span>}
    description={[runtime.provider || 'Runtime from launch profiles', runtime.model, runtime.project].filter(Boolean).join(' · ')}
    badge={agent.launches ? <span className="font-mono dash-label text-fg-muted">{agent.launches.length} launch profiles</span> : <LiveBadge live={agent.live} />}
  /></li>
}

function Fact({ k, v }: { k: string; v?: ReactNode }) {
  return <AdminFact label={k}>{v ?? <span className="text-fg-faint">not reported</span>}</AdminFact>
}

function None({ children }: { children: ReactNode }) {
  return <p className="dash-body text-fg-faint">{children}</p>
}

export function AgentDetail({ agent, now }: { agent: AgentRecord; now: number }) {
  const { identity, runtime, policy, source, live } = agent
  return (
    <article aria-label={`${identity.name} detail`}>
      <header className="flex items-center gap-4 border-b border-border/80 px-5 py-4">
        <span className="shrink-0 border border-border bg-bg p-1.5">
          <IdentityAvatar identity={identity.id} size="large" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="truncate font-mono dash-detail-title font-semibold uppercase tracking-wide">{identity.name}</h2>
          <div className="truncate font-mono dash-support text-fg-faint">{identity.id}{identity.urn ? ` · ${identity.urn}` : ' · address not reported'}</div>
          {identity.summary && <p className="mt-1 dash-body text-fg-muted">{identity.summary}</p>}
        </div>
      </header>

      <Section title={agent.launches ? 'Operations' : 'Live state'}>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1">
          {agent.launches ? <span className="dash-body text-fg-muted">Live agents and processes are managed on Agents.</span> : <LiveBadge live={live} />}
          {live && <span className="font-mono dash-support text-fg-muted">{live.sessions} session{live.sessions === 1 ? '' : 's'}</span>}
          {live?.lastActivity != null && <span className="font-mono dash-support text-fg-muted">active {ago(live.lastActivity, now)}</span>}
          <Link to={FLEET_PATH} className="ml-auto font-mono dash-label uppercase tracking-widest text-warning hover:underline">{agent.launches ? 'Open Agents' : 'open in fleet'}</Link>
        </div>
      </Section>

      <Section title="Identity">
        <dl>
          <Fact k="Status" v={identity.enabled ? 'Enabled' : 'Disabled'} />
          <Fact k="Roles" v={identity.roles.length ? <span className="flex flex-wrap gap-1.5">{identity.roles.map((r) => <Chip key={r}>{r}</Chip>)}</span> : undefined} />
        </dl>
      </Section>

      <Section title="Runtime">
        <dl>
          <Fact k="Provider" v={runtime.provider || 'Runtime from launch profiles'} />
          <Fact k="Model" v={runtime.model} />
          <Fact k="Project" v={runtime.project} />
          <Fact k="Worker" v={runtime.worker} />
          <Fact k="Workspace" v={runtime.workspaceMode} />
        </dl>
      </Section>

      {agent.launches && <Section title="Launch profiles">
        {agent.launches.length ? <details open={agent.launches.length <= 3}><summary className="mb-2 cursor-pointer dash-body text-fg-muted">{agent.launches.length} launch profiles</summary><ul className="space-y-2">{agent.launches.map((l) => <li key={l.id} className="dash-body"><span className="font-mono text-fg">{l.id}</span><span className="ml-3 text-fg-muted">{[l.provider, l.project, l.enabled === false ? 'disabled' : ''].filter(Boolean).join(' · ')}</span></li>)}</ul></details> : <None>No launch profiles reference this agent.</None>}
      </Section>}
      <Section title="Prompts">
        {runtime.systemPrompt && <details><summary className="cursor-pointer dash-body text-fg-muted">System prompt</summary><pre className="mt-2 whitespace-pre-wrap break-words dash-support text-fg-muted">{runtime.systemPrompt}</pre></details>}
        {runtime.prompt && <details className="mt-2"><summary className="cursor-pointer dash-body text-fg-muted">Agent prompt</summary><pre className="mt-2 whitespace-pre-wrap break-words dash-support text-fg-muted">{runtime.prompt}</pre></details>}
        {!runtime.systemPrompt && !runtime.prompt && <None>No prompts reported.</None>}
      </Section>
      <Section title="Tools and skills">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="w-16 font-mono dash-label uppercase tracking-wider text-fg-faint">tools</span>
            {agent.tools.length ? agent.tools.map((t) => <Chip key={t} tone="border-success/50 text-success">{t}</Chip>) : <None>None resolved.</None>}
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="w-16 font-mono dash-label uppercase tracking-wider text-fg-faint">skills</span>
            {agent.skills.length ? agent.skills.map((t) => <Chip key={t}>{t}</Chip>) : <None>No skills equipped.</None>}
          </div>
        </div>
        <p className="mt-2 dash-support text-fg-faint">Tools come from the home project, not the agent.</p>
      </Section>

      <Section title="Permissions and policy">
        <p className={`mb-2 dash-body ${isPrivileged(agent) ? 'text-warning' : 'text-fg-muted'}`}>{policySummary(agent)}</p>
        <dl>
          <Fact k="Permission" v={policy.permissionMode} />
          <Fact k="Sandbox" v={policy.sandbox} />
          <Fact k="Network" v={policy.network === undefined ? undefined : policy.network ? 'Online' : 'Offline'} />
          <Fact k="Scopes" v={policy.scopes.length ? <span className="flex flex-wrap gap-1.5">{policy.scopes.map((s) => <Chip key={s} tone="border-warning/60 text-warning">{s}</Chip>)}</span> : 'None'} />
          <Fact k="Rules" v={policy.rules.length ? <span className="flex flex-wrap gap-1.5">{policy.rules.map((s) => <Chip key={s}>{s}</Chip>)}</span> : 'None'} />
        </dl>
      </Section>

      <Section title="Teams">
        {agent.teams.length ? (
          <ul>
            {agent.teams.map((t) => (
              <li key={t.teamId} className="flex items-baseline gap-3 py-0.5 dash-body">
                <span className="text-fg">{t.teamName}</span>
                <span className="font-mono dash-label uppercase tracking-wider text-fg-faint">{t.role}</span>
              </li>
            ))}
          </ul>
        ) : (
          <None>{agent.launches ? 'Configure team seats under Teams. Live memberships are managed separately.' : 'Not on any team.'}</None>
        )}
      </Section>

      <Section title="Source definition">
        <dl>
          <Fact k={agent.launches ? "Catalog entry" : "File"} v={<span className="font-mono dash-support">{source.path}</span>} />
          <Fact k="Revision" v={source.revision && <span className="font-mono dash-support">{source.revision}</span>} />
          <Fact k="Updated" v={source.updatedAt ? ago(source.updatedAt, now) : undefined} />
        </dl>
      </Section>
    </article>
  )
}
