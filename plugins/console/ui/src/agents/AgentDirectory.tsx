import { AdminInput, AdminSelect } from '@hollis-labs/kit-admin'
import { dataRefreshRegistry } from "@tachyon/host-sdk"
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { EmptyState } from '@hollis-labs/design-components'
import { AdminDirectory, AdminRecordButton, AdminSection as Section, AdminFact, AdminTabs, IdentityAvatar, ActivityStatus } from '@hollis-labs/kit-admin'
import { DashboardToolbar } from '@hollis-labs/kit-dashboard/layout'
import { ago } from '@console/fleet/lib/agent-activity'
import { AgentEditor } from './AgentEditor'
import { AssignmentsView } from './AssignmentsView'
import { AuditTrail } from './AuditTrail'
import { CreateWizard } from './CreateWizard'
import { LifecyclePanel } from './LifecyclePanel'
import { TeamsView } from './TeamsView'
import { TeamBuilder } from './TeamBuilder'
import { useAgentsClient } from './context'
import { AgentEditProvider, useAgentEdit } from './edit-context'
import { Btn, Chip } from './ui'
import { filterAgents, isPrivileged, policySummary } from './filter'
import type { AgentLiveRef, AgentRecord } from './types'

const LIVE_LABEL: Record<AgentLiveRef['state'], string> = { waiting: 'WAITING ON YOU', busy: 'WORKING', idle: 'IDLE', offline: 'OFFLINE' }

const FLEET_PATH = '/agents'

function LiveBadge({ live, unknown }: { live?: AgentLiveRef; unknown?: boolean }) {
  return <ActivityStatus label={live ? LIVE_LABEL[live.state] : unknown ? 'NOT CONNECTED' : 'NOT RUNNING'} tone={live?.state === 'busy' ? 'positive' : live?.state === 'waiting' ? 'attention' : live?.state === 'idle' ? 'neutral' : 'muted'} />
}

function Row({ agent, selected, onSelect }: { agent: AgentRecord; selected: boolean; onSelect: () => void }) {
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

/** Read-only agent directory: list on the left, detail on the right. No stream hooks; the client is the only data seam. */
export function AgentDirectoryView(props: { now?: number } = {}) {
  // Provide the editor wiring (ActionBus + IdentityConfirm) unless a parent already did.
  const wired = useAgentEdit()
  return wired ? <AgentDirectoryInner {...props} /> : <AgentEditProvider><AgentDirectoryInner {...props} /></AgentEditProvider>
}

type PaneMode = 'view' | 'edit' | 'lifecycle' | 'audit'
type Section = 'agents' | 'teams' | 'assignments'

function AgentDirectoryInner({ now: nowProp }: { now?: number } = {}) {
  const client = useAgentsClient()
  const [agents, setAgents] = useState<AgentRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [reloadVersion, setReloadVersion] = useState(0)
  const [search, setSearch] = useState('')
  const [provider, setProvider] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [mode, setMode] = useState<PaneMode>('view')
  const [auditVersion, setAuditVersion] = useState(0)
  const [section, setSection] = useState<Section>('agents')
  const [visited, setVisited] = useState<Set<Section>>(new Set(['agents']))
  const [creating, setCreating] = useState<{ cloneOf?: string } | null>(null)
  const [showRetired, setShowRetired] = useState(false)
  const [now] = useState(() => nowProp ?? Date.now())

  useEffect(() => {
    let active = true
    client
      .listAgents()
      .then((rows) => active && (setAgents(rows), setError(null)))
      .catch((e) => active && setError(e instanceof Error ? e.message : 'Agent catalog unavailable'))
    return () => {
      active = false
    }
  }, [client, reloadVersion])

  useEffect(() => dataRefreshRegistry('catalog').subscribe(() => setReloadVersion(v => v + 1)), [])

  const reload = () => client.listAgents().then((rows) => setAgents(rows), () => undefined)

  const providers = useMemo(() => [...new Set((agents ?? []).flatMap((a) => [a.runtime.provider, ...(a.launches ?? []).map((l) => l.provider ?? '')]).filter(Boolean))].sort(), [agents])
  const rows = useMemo(() => filterAgents(agents ?? [], { search, provider }).filter((a) => showRetired || !a.identity.retired || a.identity.id === selectedId), [agents, search, provider, showRetired, selectedId])

  if (agents === null) {
    return error ? (
      <div className="p-4"><EmptyState variant="error" title="Agent catalog unavailable" description={error} /><Btn onClick={() => { setError(null); setReloadVersion((v) => v + 1) }}>Retry catalog read</Btn></div>
    ) : (
      <div className="p-4 font-mono dash-support text-fg-faint">Loading agents…</div>
    )
  }

  const selected = rows.find((a) => a.identity.id === selectedId) ?? rows[0] ?? null
  const live = agents.filter((a) => a.live && a.live.state !== 'offline').length

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-border/80 px-4 py-2 font-mono dash-label uppercase tracking-widest text-fg-faint">
        <span>{agents.length} agent definitions</span>
        {client.writeMode !== 'demonstration' && <span className={live ? 'text-success' : ''}>{live} running</span>}
        <span>{agents.filter((a) => !a.identity.enabled && !a.identity.retired).length} disabled</span>
        {client.writeMode !== 'demonstration' && <span>{agents.filter((a) => a.identity.retired).length} retired</span>}
        <span className="ml-auto text-fg-muted">{client.writeMode === 'demonstration' ? 'Tether catalog · demonstration saves' : 'Agent catalog'}</span>
      </div>
      {client.writeMode === 'demonstration' && <p className="border-b border-warning/30 bg-warning/5 px-4 py-3 dash-body text-warning">Save actions are no-op demonstrations pending write access. Nothing is written to Tether or Torque.</p>}
      <AdminTabs label="Builder configuration" value={section}
        onChange={sec => { setSection(sec); setVisited(v => new Set([...v, sec])); setCreating(null) }}
        items={[{ key: 'agents', label: 'Definitions' }, { key: 'teams', label: 'Teams' }, { key: 'assignments', label: 'Assignments' }]}
        actions={section === 'agents' && <Btn tone="primary" onClick={() => { setSection('agents'); setCreating({}) }}>New agent</Btn>}
      />
      {creating && (
        <CreateWizard
          agents={agents}
          cloneOf={creating.cloneOf}
          onClose={() => { setCreating(null); void reload() }}
          onCreated={(a) => { setSelectedId(a.identity.id); setMode('view'); setAuditVersion((v) => v + 1); void reload() }}
        />
      )}
      {visited.has('teams') && <div hidden={!!creating || section !== 'teams'}>{client.writeMode === 'demonstration' ? <TeamBuilder /> : <TeamsView agents={agents} onAgentsChanged={() => void reload()} />}</div>}
      {visited.has('assignments') && <div hidden={!!creating || section !== 'assignments'}><AssignmentsView agents={agents} now={nowProp} /></div>}
      {!creating && section === 'agents' && (<>
      <DashboardToolbar className="border-b border-border px-4 py-2">
        <AdminInput value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search agents" aria-label="Search agents" className="max-w-xs" />
        <AdminSelect value={provider} onChange={(e) => setProvider(e.target.value)} aria-label="Filter by provider" className="w-auto">
          <option value="">All providers</option>
          {providers.map((p) => <option key={p} value={p}>{p}</option>)}
        </AdminSelect>
        {client.writeMode !== 'demonstration' && <label className="flex items-center gap-2 font-mono dash-label uppercase tracking-widest text-fg-faint"><input type="checkbox" checked={showRetired} onChange={(e) => setShowRetired(e.target.checked)} /> Show retired</label>}
      </DashboardToolbar>
      {rows.length === 0 ? (
        <div className="p-4"><EmptyState variant="empty" title="No agents match" description="Clear the search or provider filter." /></div>
      ) : (
        <AdminDirectory label="Agent definitions" layout="wide" records={
          <ul aria-label="Agents">
            {rows.map((a) => <Row key={a.identity.id} agent={a} selected={a.identity.id === selected?.identity.id} onSelect={() => { setSelectedId(a.identity.id); setMode('view') }} />)}
          </ul>
          } detail={selected && (
            <div>
              <AdminTabs label="Agent pane" value={mode} onChange={setMode}
                items={[{ key: 'view', label: 'Details' }, { key: 'edit', label: 'Edit' }, { key: 'lifecycle', label: 'Lifecycle' }, { key: 'audit', label: 'Audit', disabled: client.writeMode === 'demonstration', title: client.writeMode === 'demonstration' ? 'Audit API not connected yet' : undefined }]} />
              {mode === 'view' && <AgentDetail agent={selected} now={now} />}
              {mode === 'edit' && (
                <AgentEditor
                  key={selected.identity.id}
                  agent={selected}
                  onClose={() => setMode('view')}
                  onChanged={(next) => {
                    setAgents((cur) => cur && cur.map((a) => (a.identity.id === next.identity.id ? { ...next, live: a.live } : a)))
                    setAuditVersion((v) => v + 1)
                  }}
                />
              )}
              {mode === 'lifecycle' && (
                <LifecyclePanel
                  key={selected.identity.id}
                  agent={selected}
                  onClone={(id) => setCreating({ cloneOf: id })}
                  onChanged={(next) => {
                    setAgents((cur) => cur && cur.map((a) => (a.identity.id === next.identity.id ? { ...next, live: next.live ?? a.live } : a)))
                    setAuditVersion((v) => v + 1)
                    void reload()
                  }}
                />
              )}
              {mode === 'audit' && <AuditTrail agentId={selected.identity.id} version={auditVersion} />}
            </div>
          )} />
      )}
      </>)}
    </div>
  )
}
