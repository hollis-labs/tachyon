import { useState } from 'react'
import type { ConsoleEvent } from '../events/types'
import { ago } from '../lib/agent-activity'
import { activityText, workStateText } from './model'
import { useObservations } from './store'

function description(e: ConsoleEvent) {
  if (e.kind === 'session.work_state_changed') return `Session ${workStateText({ working: e.payload.to === 'processing' })!.toLowerCase()}`
  return activityText(e) || e.kind.replaceAll('.', ' ').replaceAll('_', ' ')
}
const FIELDS = ['tool', 'server', 'duration_ms', 'error_class', 'reason', 'runtime', 'message_id', 'thread_id', 'turn_id', 'project_id', 'workstream_id', 'worker_id', 'from', 'to']
function EventRow({ event: e, now }: { event: ConsoleEvent; now: number }) {
  return <li className="border-b border-border/70 last:border-0">
    <details className="group">
      <summary className="flex cursor-pointer items-baseline gap-3 px-3 py-2 text-control hover:bg-surface focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
        <span aria-hidden className="text-fg-faint group-open:rotate-90">›</span>
        <span className={`min-w-0 flex-1 truncate ${e.severity === 'error' ? 'text-danger' : 'text-fg-secondary'}`}>{description(e)}</span>
        {typeof e.payload.duration_ms === 'number' && <span className="font-mono text-caption text-fg-muted">{e.payload.duration_ms} ms</span>}
        <time className="shrink-0 font-mono text-caption text-fg-faint" dateTime={new Date(e.ts).toISOString()} title={new Date(e.ts).toLocaleString()}>{ago(e.ts, now)}</time>
      </summary>
      <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-1 border-t border-border/50 bg-surface/50 px-6 py-2 font-mono text-caption">
        <dt className="text-fg-faint">Event</dt><dd className="break-all text-fg-muted">{e.kind} · {e.id}</dd>
        {e.sessionId && <><dt className="text-fg-faint">Session</dt><dd className="break-all text-fg-muted">{e.sessionId}</dd></>}
        {FIELDS.flatMap(key => {
          const value = e.payload[key]
          const text = typeof value === 'string' || typeof value === 'number' ? String(value) : Array.isArray(value) ? value.filter(v => typeof v === 'string').join(', ') : ''
          return text ? [<dt key={key} className="text-fg-faint">{key.replaceAll('_', ' ')}</dt>, <dd key={key + '-value'} className="break-words text-fg-muted">{text.slice(0, 500)}</dd>] : []
        })}
      </dl>
    </details>
  </li>
}

export function RecentActivity({ sessionId, limit = 40 }: { sessionId?: string; limit?: number }) {
  const state = useObservations()
  const [filter, setFilter] = useState('all')
  const rows = state.events.filter(e => (!sessionId || e.sessionId === sessionId) && (filter === 'all' || e.kind.startsWith(filter))).slice(0, limit)
  return <section aria-label={sessionId ? 'Recent session events' : 'Recent Tether events'} className="border-t border-border">
    <header className="flex flex-wrap items-center gap-3 px-3 py-2">
      <h3 className="font-mono text-caption uppercase tracking-widest text-fg-muted">Recent {sessionId ? 'session' : 'Tether'} events</h3>
      <select aria-label="Event type" value={filter} onChange={e => setFilter(e.target.value)} className="ml-auto border border-border bg-bg px-2 py-1 font-mono text-caption text-fg-secondary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
        <option value="all">All activity</option><option value="tool.">Tools</option><option value="message.">Messages</option><option value="session.">Sessions</option><option value="fleet.">Machines</option>
      </select>
    </header>
    <p className="px-3 pb-2 text-caption text-fg-faint">Recent window · refreshes every 5s{state.limited ? ' · some older events omitted' : ''}{state.error ? ' · refresh unavailable; showing last observations' : ''}</p>
    <ul className="max-h-72 overflow-y-auto overscroll-contain border-t border-border/70">
      {rows.map(e => <EventRow key={e.id} event={e} now={Date.now()} />)}
      {!rows.length && <li className="px-3 py-3 text-control text-fg-muted">{state.error ? 'Activity unavailable.' : !state.checkedAt ? 'Loading activity…' : 'No matching events in this recent window.'}</li>}
    </ul>
  </section>
}
