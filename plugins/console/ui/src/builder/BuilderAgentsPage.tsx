import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Segmented } from '../components/segmented'
import { AgentDetailPane } from './AgentDetailPane'
import { type AgentRow, builderApi } from './api'
import { useLoad } from './useLoad'
import { type AgentState, BUILDER_TITLE, DEFINITIONS_NOTE, POLICY_LABEL, STATE_LABEL, TAB_LABEL, type BuilderTab } from './vocab'

const CHIP = 'inline-flex items-center border border-border px-1.5 py-px font-mono text-caption text-fg-secondary'

function Rows({ rows, selected, onSelect }: { rows: AgentRow[]; selected?: string; onSelect: (id: string) => void }) {
  return (
    <ul aria-label="Agents" className="divide-y divide-border-subtle">
      {rows.map((r) => (
        <li key={r.id}>
          <button type="button" aria-current={r.id === selected ? 'true' : undefined} onClick={() => onSelect(r.id)}
            className={`flex w-full items-center gap-2 px-4 py-2 text-left hover:bg-surface-hover ${r.id === selected ? 'bg-surface' : ''}`}>
            <span aria-hidden className="flex h-7 w-7 shrink-0 items-center justify-center border border-border font-mono dash-support text-fg-muted">{r.name.slice(0, 2).toUpperCase()}</span>
            <span className="min-w-0 flex-1">
              <span className="block truncate dash-body text-fg">{r.name}</span>
              <span className="block truncate font-mono dash-support text-fg-muted">{r.launch_count} {r.launch_count === 1 ? 'launch' : 'launches'}</span>
            </span>
            {r.policy_tag && <span className={CHIP}>{POLICY_LABEL[r.policy_tag]}</span>}
            {r.state !== 'active' && <span className={CHIP}>{STATE_LABEL[r.state]}</span>}
          </button>
        </li>
      ))}
    </ul>
  )
}

/** Builder > Agents on the /builder/* read model. Editing and Review arrive with the changes flow. */
export function BuilderAgentsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [debounced, setDebounced] = useState('')
  const [provider, setProvider] = useState('')
  const [state, setState] = useState<AgentState | ''>('')
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q), 250)
    return () => clearTimeout(t)
  }, [q])

  const [summary] = useLoad(builderApi.summary, [])
  const [list, retry] = useLoad(() => builderApi.agents({ q: debounced, provider, state: state || undefined }), [debounced, provider, state])
  const counts = summary.status === 'ready' ? summary.data : undefined
  const retiredShown = counts?.capabilities?.retired !== false

  const rows = list.status === 'ready' ? list.data.agents : []
  const providers = useMemo(() => [...new Set(rows.map((r) => r.provider).filter(Boolean))].sort(), [rows])
  const select = (next: string) => navigate(`/builder/agents/${encodeURIComponent(next)}`)

  // On wide screens something is always selected once the list exists.
  useEffect(() => {
    if (!id && rows.length > 0 && window.matchMedia?.('(min-width: 768px)').matches) navigate(`/builder/agents/${encodeURIComponent(rows[0].id)}`, { replace: true })
  }, [id, rows, navigate])

  const states: (AgentState | '')[] = retiredShown ? ['', 'active', 'disabled', 'retired'] : ['', 'active', 'disabled']
  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className="flex flex-wrap items-baseline gap-x-4 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold text-fg">{BUILDER_TITLE}</h1>
        <p className="dash-support text-fg-muted">{DEFINITIONS_NOTE} <Link className="underline" to="/agents">Agents</Link>.</p>
      </header>
      <Segmented<BuilderTab> label="Builder areas" value="agents" onChange={() => undefined}
        items={[{ value: 'agents', label: TAB_LABEL.agents, count: counts?.agents }, { value: 'launches', label: `${TAB_LABEL.launches} (soon)`, count: counts?.launches }, { value: 'library', label: `${TAB_LABEL.library} (soon)` }]} />
      <div className="flex min-h-0 flex-1">
        <div className={`${id ? 'hidden md:flex' : 'flex'} w-full shrink-0 flex-col border-r border-border md:w-80`}>
          <div className="flex flex-wrap gap-2 border-b border-border p-3">
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search agents" aria-label="Search agents" className="min-w-0 flex-1 border border-border bg-bg px-2 py-1 dash-body text-fg" />
            <select value={provider} onChange={(e) => setProvider(e.target.value)} aria-label="Filter by provider" className="border border-border bg-bg px-2 py-1 dash-body text-fg">
              <option value="">All providers</option>
              {providers.map((p) => <option key={p} value={p}>{p}</option>)}
            </select>
            <div role="group" aria-label="State" className="flex gap-1">
              {states.map((s) => (
                <button key={s || 'all'} type="button" aria-pressed={state === s} onClick={() => setState(s)} className={`px-2 py-0.5 dash-control ${state === s ? 'bg-surface font-medium text-fg' : 'text-fg-muted hover:text-fg'}`}>{s ? STATE_LABEL[s] : 'All'}</button>
              ))}
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {list.status === 'loading' && <div aria-busy="true" className="space-y-2 p-4">{[0, 1, 2, 3].map((i) => <div key={i} className="h-10 animate-pulse bg-surface" />)}</div>}
            {list.status === 'error' && (
              <div role="alert" className="space-y-2 p-4">
                <p className="dash-body text-fg-secondary">{list.message}</p>
                <button type="button" onClick={retry} className="border border-border px-2 py-1 dash-control text-fg-secondary hover:bg-surface-hover">Retry</button>
              </div>
            )}
            {list.status === 'ready' && rows.length === 0 && (
              <p className="p-4 dash-body text-fg-muted">{q || provider || state ? 'No agents match. Clear the search or filters.' : 'No agents yet.'}</p>
            )}
            {list.status === 'ready' && rows.length > 0 && <Rows rows={rows} selected={id} onSelect={select} />}
          </div>
        </div>
        <div className={`${id ? 'flex' : 'hidden md:flex'} min-w-0 flex-1 flex-col`}>
          {id ? <AgentDetailPane key={id} id={id} onBack={() => navigate('/builder/agents')} /> : <p className="p-6 dash-body text-fg-muted">Select an agent.</p>}
        </div>
      </div>
    </div>
  )
}
