import { useEffect, useState } from 'react'
import { BuilderShell } from './BuilderShell'
import { type Launch, builderApi } from './api'
import { useLoad } from './useLoad'
import { NOT_REPORTED } from './vocab'

const CHIP = 'inline-flex items-center border border-border px-1.5 py-px font-mono text-caption text-fg-secondary'

function LaunchRow({ l }: { l: Launch }) {
  const secrets = l.env?.secret_like_count ?? 0
  return (
    <li className="space-y-1 px-4 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="dash-body font-medium text-fg">{l.agent}</span>
        <span className="dash-support text-fg-muted">
          {l.project} on {l.worker}
        </span>
        {!l.enabled && <span className={CHIP}>Disabled</span>}
        {l.live_count > 0 && <span className={CHIP}>{l.live_count} live</span>}
      </div>
      <p className="dash-support text-fg-muted">
        {l.provider}
        {l.model ? ` · ${l.model}` : ''} · Route: {l.route || NOT_REPORTED} · Chat: {l.chat === undefined ? NOT_REPORTED : l.chat ? 'on' : 'off'}
      </p>
      <p className="dash-support text-fg-muted">
        Environment: {l.env?.keys.length ? l.env.keys.join(', ') : 'no keys'}
        {secrets > 0 ? ` (${secrets} look secret; values are never shown)` : ''}
      </p>
    </li>
  )
}

/** Catalog launches, read-only. Starting a session is not offered here yet. */
export function BuilderLaunchesPage() {
  const [q, setQ] = useState('')
  const [live, setLive] = useState(false)
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q), 250)
    return () => clearTimeout(t)
  }, [q])
  const [load, retry] = useLoad(() => builderApi.launches({ q: debounced, live }), [debounced, live])
  return (
    <div className="flex h-full min-w-0 flex-col">
      <BuilderShell tab="launches" />
      <div className="flex flex-wrap items-center gap-3 border-b border-border p-3">
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search launches"
          aria-label="Search launches"
          className="min-w-0 flex-1 border border-border bg-bg px-2 py-1 dash-body text-fg"
        />
        <label className="flex items-center gap-2 dash-control text-fg-secondary">
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> Live only
        </label>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {load.status === 'loading' && (
          <div aria-busy="true" className="space-y-2 p-4">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-14 animate-pulse bg-surface" />
            ))}
          </div>
        )}
        {load.status === 'error' && (
          <div role="alert" className="space-y-2 p-4">
            <p className="dash-body text-fg-secondary">{load.message}</p>
            <button type="button" onClick={retry} className="border border-border px-2 py-1 dash-control text-fg-secondary hover:bg-surface-hover">
              Retry
            </button>
          </div>
        )}
        {load.status === 'ready' &&
          (load.data.launches.length === 0 ? (
            <p className="p-4 dash-body text-fg-faint">No launches match.</p>
          ) : (
            <ul aria-label="Launches" className="divide-y divide-border-subtle">
              {load.data.launches.map((l) => (
                <LaunchRow key={l.id} l={l} />
              ))}
            </ul>
          ))}
      </div>
    </div>
  )
}
