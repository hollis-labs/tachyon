import { useMemo, useState } from 'react'
import { Button, SearchInput, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@hollis-labs/design-components'
import { useNavigate } from 'react-router-dom'
import { BuilderShell } from './BuilderShell'
import { type Launch, builderApi, errorMessage } from './api'
import { newRequestId } from './changes'
import { EnvDialog } from './EnvDialog'
import { EntityDialog, LAUNCH_FIELDS } from './EntityDialog'
import { useChangeMode } from './capabilities'
import { Cite, Field, Muted, RailCard } from './parts'
import { useLoad } from './useLoad'
import { NOT_REPORTED } from './vocab'

const ALL = '__all'
const COLS = 'grid-cols-[1.5fr_1fr_.8fr] sm:grid-cols-[1.6fr_1.2fr_.8fr_1fr_.7fr_.7fr_.4fr]'

/** One filter chip: a kit Select shaped as a small pill. */
function FilterChip({ label, value, options, onChange }: { label: string; value: string; options: string[]; onChange: (v: string) => void }) {
  return (
    <Select value={value === '' ? ALL : value} onValueChange={(v) => onChange(v === null || v === ALL ? '' : String(v))}>
      <SelectTrigger className="h-6 w-auto shrink-0 gap-1 rounded-full px-2.5 py-0 text-label data-[size=default]:h-6" aria-label={`Filter by ${label.toLowerCase()}`}>
        <SelectValue>{(v: unknown) => (v === ALL || v == null ? label : String(v))}</SelectValue>
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ALL}>{label}</SelectItem>
        {options.map((o) => (
          <SelectItem key={o} value={o}>
            {o}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function Dot({ on }: { on: boolean }) {
  return <i aria-hidden className={`inline-block size-[7px] shrink-0 rounded-full ${on ? 'bg-fg opacity-90' : 'bg-fg-faint opacity-60'}`} />
}

const launchValues = (l: Launch): Record<string, string | boolean> => ({
  agent: l.agent,
  provider: l.provider,
  project: l.project,
  worker: l.worker,
  'workspace.mode': l.workspace?.mode ?? '',
  route: l.route ?? '',
  chat: l.chat === true,
  enabled: l.enabled,
})

function LaunchDetail({ l, agentName, onChanged }: { l: Launch; agentName: string; onChanged: () => void }) {
  const [mode] = useChangeMode()
  const [dialog, setDialog] = useState<'edit' | 'duplicate' | 'env'>()
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const secrets = l.env?.secret_like_count ?? 0
  const keys = l.env?.keys.length ?? 0
  const start = async () => {
    setBusy(true)
    setError('')
    try {
      const r = await builderApi.launch(l.id, newRequestId())
      navigate(r.agents_url ?? '/agents')
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <RailCard title={l.id}>
      <dl className="flex flex-col gap-2">
        <Field label="Agent" stacked>
          <Cite to={`/builder/agents/${encodeURIComponent(l.agent)}`}>{agentName}</Cite>
        </Field>
        <Field label="Provider · model" stacked>
          {l.provider}
          {l.model ? ` · ${l.model}` : ''}
        </Field>
        <Field label="Project" stacked>
          <Cite to={`/registry/projects/${encodeURIComponent(l.project)}`}>{l.project}</Cite>
        </Field>
        <Field label="Worker" stacked>
          {l.worker || NOT_REPORTED}
        </Field>
        <Field label="Workspace mode" stacked>
          {l.workspace?.mode ?? NOT_REPORTED}
        </Field>
        <Field label="Environment" stacked>
          {l.env ? `${keys} ${keys === 1 ? 'variable' : 'variables'} · ${secrets} ${secrets === 1 ? 'secret' : 'secrets'}` : NOT_REPORTED}
        </Field>
      </dl>
      <div className="flex items-center gap-2">
        {mode !== 'unavailable' && (
          <>
            <Button type="button" variant="outline" size="sm" onClick={() => setDialog('edit')}>
              Edit
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => setDialog('duplicate')}>
              Duplicate
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => setDialog('env')}>
              Set variable
            </Button>
          </>
        )}
        <span className="flex-1" />
        <Button type="button" size="sm" disabled={busy || !l.enabled} onClick={start}>
          {busy ? 'Launching' : 'Launch ▸'}
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-xs text-fg-secondary">
          Could not launch. {error}
        </p>
      )}
      <Muted>Launching opens the session on Agents. Saving never changes a running session.</Muted>
      {dialog === 'env' && <EnvDialog launchId={l.id} sha={l.sha} onClose={() => setDialog(undefined)} onApplied={onChanged} />}
      {dialog && dialog !== 'env' && (
        <EntityDialog
          kind="launch"
          mode={dialog === 'edit' ? 'edit' : 'create'}
          noun="launch"
          fields={LAUNCH_FIELDS}
          initial={launchValues(l)}
          id={dialog === 'edit' ? l.id : undefined}
          sha={l.sha}
          onClose={() => setDialog(undefined)}
          onApplied={onChanged}
        />
      )}
    </RailCard>
  )
}

/** Catalog launches: filter bar, table and the selected launch in a right rail. */
export function BuilderLaunchesPage() {
  const [q, setQ] = useState('')
  const [live, setLive] = useState(false)
  const [provider, setProvider] = useState('')
  const [project, setProject] = useState('')
  const [worker, setWorker] = useState('')
  const [picked, setPicked] = useState<string>()
  const [creating, setCreating] = useState(false)
  const [mode] = useChangeMode()
  const [load, retry] = useLoad(() => builderApi.launches({ q, live }), [q, live])
  const [agents] = useLoad(() => builderApi.agents({}), [])
  const names = useMemo(() => new Map((agents.status === 'ready' ? agents.data.agents : []).map((a) => [a.id, a.name])), [agents])
  const all = load.status === 'ready' ? load.data.launches : []
  const uniq = (pick: (l: Launch) => string) => [...new Set(all.map(pick).filter(Boolean))].sort()
  const rows = all.filter((l) => (!provider || l.provider === provider) && (!project || l.project === project) && (!worker || l.worker === worker))
  const sel = rows.find((l) => l.id === picked) ?? rows[0]
  const nameOf = (l: Launch) => names.get(l.agent) ?? l.agent
  return (
    <div className="flex h-full min-w-0 flex-col">
      <BuilderShell tab="launches" />
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <div className="dash-search-adapter w-full min-w-0 sm:w-[260px] [&_div]:min-w-0">
          <SearchInput value={q} onChange={setQ} placeholder="Search launches" ariaLabel="Search launches" />
        </div>
        <FilterChip label="Provider" value={provider} options={uniq((l) => l.provider)} onChange={setProvider} />
        <FilterChip label="Project" value={project} options={uniq((l) => l.project)} onChange={setProject} />
        <FilterChip label="Worker" value={worker} options={uniq((l) => l.worker)} onChange={setWorker} />
        <button
          type="button"
          aria-pressed={live}
          onClick={() => setLive((v) => !v)}
          className={`inline-flex h-6 items-center rounded-full border px-2.5 text-label ${live ? 'border-border bg-selection text-fg' : 'border-border text-fg-secondary hover:text-fg'}`}
        >
          Live only
        </button>
        <span className="flex-1" />
        {mode !== 'unavailable' && (
          <Button type="button" size="sm" onClick={() => setCreating(true)} className="hidden sm:inline-flex">
            New launch
          </Button>
        )}
        {creating && <EntityDialog kind="launch" mode="create" noun="launch" fields={LAUNCH_FIELDS} initial={{ chat: false, enabled: true }} onClose={() => setCreating(false)} onApplied={retry} />}
      </div>
      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1 overflow-auto">
          {load.status === 'loading' && (
            <div aria-busy="true" className="space-y-2 p-4">
              {[0, 1, 2].map((i) => (
                <div key={i} className="h-9 animate-pulse rounded-control bg-surface" />
              ))}
            </div>
          )}
          {load.status === 'error' && (
            <div role="alert" className="space-y-2 p-4">
              <p className="text-control text-fg-secondary">{load.message}</p>
              <Button type="button" onClick={retry} variant="outline" size="sm">
                Retry
              </Button>
            </div>
          )}
          {load.status === 'ready' && rows.length === 0 && (
            <p className="p-4 text-control text-fg-muted">{q || live || provider || project || worker ? 'No launches match. Clear the search or filters.' : 'No launches yet.'}</p>
          )}
          {load.status === 'ready' && rows.length > 0 && (
            <div role="table" aria-label="Launches">
              <div role="row" className={`grid ${COLS} items-center gap-3 px-4 py-2.5 font-mono text-caption uppercase tracking-label text-fg-muted`}>
                {['Launch', 'Agent', 'Provider', 'Project', 'Worker', 'Mode', 'Live'].map((h, i) => (
                  <span key={h} role="columnheader" className={i > 2 ? 'hidden sm:block' : ''}>
                    {h}
                  </span>
                ))}
              </div>
              {rows.map((l) => (
                <div
                  key={l.id}
                  role="row"
                  tabIndex={0}
                  aria-selected={l.id === sel?.id}
                  onClick={() => setPicked(l.id)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      setPicked(l.id)
                    }
                  }}
                  className={`grid w-full ${COLS} cursor-pointer items-center gap-3 border-t border-border-subtle px-4 py-2.5 text-left text-xs outline-none hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring ${l.id === sel?.id ? 'bg-selection' : ''}`}
                >
                  <span role="cell" className="flex min-w-0 items-center gap-2 font-mono">
                    <Dot on={l.enabled} />
                    <span className="truncate">{l.id}</span>
                  </span>
                  <span role="cell" className="truncate">{nameOf(l)}</span>
                  <span role="cell" className="truncate">{l.provider}</span>
                  <span role="cell" className="hidden truncate sm:block">{l.project}</span>
                  <span role="cell" className="hidden truncate sm:block">{l.worker}</span>
                  <span role="cell" className="hidden truncate sm:block">{l.workspace?.mode ?? '—'}</span>
                  <span role="cell" className="hidden sm:block">{l.live_count > 0 ? l.live_count : '—'}</span>
                </div>
              ))}
            </div>
          )}
        </div>
        {sel && (
          <aside aria-label="Launch" className="hidden w-[360px] shrink-0 overflow-auto border-l border-border-subtle p-3.5 lg:block">
            <LaunchDetail key={sel.id} l={sel} agentName={nameOf(sel)} onChanged={retry} />
          </aside>
        )}
      </div>
    </div>
  )
}
