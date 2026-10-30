import { useEffect, useMemo, useState } from 'react'
import { Button, SearchInput, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@hollis-labs/design-components'
import { useNavigate, useParams } from 'react-router-dom'
import { Chip } from '../components/chip'
import { AgentDetailPane } from './AgentDetailPane'
import { type AgentRow, builderApi } from './api'
import { BuilderShell } from './BuilderShell'
import { Tile } from './parts'
import { useLoad } from './useLoad'
import { type AgentState, POLICY_LABEL, STATE_LABEL } from './vocab'

const ALL = '__all'

function Rows({ rows, selected, onSelect }: { rows: AgentRow[]; selected?: string; onSelect: (id: string) => void }) {
  return (
    <ul aria-label="Agents">
      {rows.map((r) => (
        <li key={r.id} className="border-t border-border-subtle first:border-t-0">
          <button
            type="button"
            aria-current={r.id === selected ? 'true' : undefined}
            onClick={() => onSelect(r.id)}
            className={`flex w-full items-center gap-3 px-3.5 py-2.5 text-left hover:bg-selection ${r.id === selected ? 'bg-selection' : ''}`}
          >
            <Tile name={r.name} />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate text-control font-semibold text-fg">{r.name}</span>
              <span className="truncate text-label text-fg-muted">
                {r.policy_tag ? `${POLICY_LABEL[r.policy_tag].toLowerCase()} · ` : ''}
                {r.launch_count} {r.launch_count === 1 ? 'launch' : 'launches'}
              </span>
            </span>
            {r.state !== 'active' && <Chip>{STATE_LABEL[r.state].toLowerCase()}</Chip>}
          </button>
        </li>
      ))}
    </ul>
  )
}

/** Builder > Agents on the /builder/* read model: list column and the selected agent. */
export function BuilderAgentsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [provider, setProvider] = useState('')
  const [state, setState] = useState<AgentState>('active')

  const [summary] = useLoad(builderApi.summary, [])
  const [list, retry] = useLoad(() => builderApi.agents({ q, provider, state }), [q, provider, state])
  const counts = summary.status === 'ready' ? summary.data : undefined
  const retiredShown = counts?.capabilities?.retired === true

  const rows = list.status === 'ready' ? list.data.agents : []
  const providers = useMemo(() => [...new Set(rows.map((r) => r.provider).filter(Boolean))].sort(), [rows])
  const select = (next: string) => navigate(`/builder/agents/${encodeURIComponent(next)}`)

  // On wide screens something is always selected once the list exists.
  useEffect(() => {
    if (!id && rows.length > 0 && window.matchMedia?.('(min-width: 641px)').matches) navigate(`/builder/agents/${encodeURIComponent(rows[0].id)}`, { replace: true })
  }, [id, rows, navigate])

  const states: AgentState[] = retiredShown ? ['active', 'disabled', 'retired'] : ['active', 'disabled']
  const countOf = (s: AgentState) => counts?.agents?.[s]
  return (
    <div className="flex h-full min-w-0 flex-col">
      <BuilderShell tab="agents" counts={counts ?? null} />
      <div className="flex min-h-0 flex-1">
        <div className={`${id ? 'hidden sm:flex' : 'flex'} w-full shrink-0 flex-col border-r border-border-subtle sm:w-[340px]`}>
          <div className="flex items-center gap-2 p-2">
            <div className="dash-search-adapter min-w-0 flex-1 [&_div]:min-w-0">
              <SearchInput value={q} onChange={setQ} placeholder="Search agents" ariaLabel="Search agents" />
            </div>
            <Select value={provider === '' ? ALL : provider} onValueChange={(v) => setProvider(v === null || v === ALL ? '' : String(v))}>
              <SelectTrigger className="h-6 w-auto shrink-0 gap-1 rounded-full px-2.5 py-0 text-label data-[size=default]:h-6" aria-label="Filter by provider">
                <SelectValue>{(v: unknown) => (v === ALL || v == null ? 'All providers' : String(v))}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All providers</SelectItem>
                {providers.map((p) => (
                  <SelectItem key={p} value={p}>
                    {p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div role="group" aria-label="State" className="flex gap-1.5 px-2 pb-2">
            {states.map((s) => (
              <button
                key={s}
                type="button"
                aria-pressed={state === s}
                onClick={() => setState(s)}
                className={`inline-flex h-5 items-center gap-1.5 rounded-full border px-2 text-label ${state === s ? 'border-border bg-selection text-fg' : 'border-border text-fg-secondary hover:text-fg'}`}
              >
                {STATE_LABEL[s]}
                {countOf(s) != null && <span className="tabular-nums">{countOf(s)}</span>}
              </button>
            ))}
          </div>
          <div className="min-h-0 flex-1 overflow-auto border-t border-border-subtle">
            {list.status === 'loading' && (
              <div aria-busy="true" className="space-y-2 p-4">
                {[0, 1, 2, 3].map((i) => (
                  <div key={i} className="h-10 animate-pulse rounded-control bg-surface" />
                ))}
              </div>
            )}
            {list.status === 'error' && (
              <div role="alert" className="space-y-2 p-4">
                <p className="text-control text-fg-secondary">{list.message}</p>
                <Button type="button" onClick={retry} variant="outline" size="sm">
                  Retry
                </Button>
              </div>
            )}
            {list.status === 'ready' && rows.length === 0 && (
              <p className="p-4 text-control text-fg-muted">{q || provider ? 'No agents match. Clear the search or filter.' : `No ${STATE_LABEL[state].toLowerCase()} agents.`}</p>
            )}
            {list.status === 'ready' && rows.length > 0 && <Rows rows={rows} selected={id} onSelect={select} />}
          </div>
        </div>
        <div className={`${id ? 'flex' : 'hidden sm:flex'} min-w-0 flex-1 flex-col`}>
          {id ? <AgentDetailPane key={id} id={id} onBack={() => navigate('/builder/agents')} /> : <p className="p-6 text-control text-fg-muted">Select an agent.</p>}
        </div>
      </div>
    </div>
  )
}
