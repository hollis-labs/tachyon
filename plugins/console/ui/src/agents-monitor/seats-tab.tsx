import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { Skeleton } from '@hollis-labs/design-components'
import { Chip } from '@planner/shared'
import { RefChip } from '../autolink'
import { useAgentsRead } from './api'
import { ageText } from './session-parts'
import { Avatar } from './session-rows'
import { SEATS } from './vocabulary'

/** GET /agents/seats (CW-20261114-0023d): each seat's declared status beside its newest live session. */
export interface Seat {
  agent_urn: string
  handle: string
  name: string
  effective: string
  declared: { state: string; reason: string; ref: string; owner: string; expires_at: string | null; set_by: string; at: string } | null
  live: { session_id: string; state: string; needs_you: string | null; last_activity_at: number | null } | null
}

const TONE: Record<string, 'danger' | 'warning' | 'neutral'> = { blocked: 'danger', 'waiting-decision': 'warning', 'waiting-agent': 'warning' }

function Ref({ r }: { r: string }) {
  if (r.startsWith('task:')) return <RefChip kind="torque.task" canonical={r.slice(5)} raw={r.slice(5)} resolve="hover" />
  return <a href={r} target="_blank" rel="noreferrer" className="font-mono text-caption text-fg-secondary underline">{r.replace(/^https:\/\//, '').slice(0, 40)}</a>
}

export function SeatCard({ seat, now }: { seat: Seat; now: number }) {
  const d = seat.declared
  return (
    <li data-seat={seat.agent_urn} className="flex flex-col gap-2 rounded-panel border border-border p-3">
      <div className="flex items-center gap-2.5">
        <Avatar name={seat.name} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-control font-medium text-fg">{seat.name}</div>
          {seat.handle && seat.handle !== seat.name && <div className="font-mono text-caption text-fg-muted">{seat.handle}</div>}
        </div>
        <Chip tone={TONE[seat.effective] ?? 'neutral'}>{SEATS.state(seat.effective)}</Chip>
      </div>
      {d?.reason && <p className="text-xs text-fg">{d.reason}</p>}
      {d && (d.owner || d.ref || d.expires_at) && (
        <div className="flex flex-wrap items-center gap-2 text-label text-fg-muted">
          {d.owner && <span>{SEATS.owner(d.owner)}</span>}
          {d.ref && <Ref r={d.ref} />}
          {d.expires_at && <span title={new Date(d.expires_at).toLocaleString()}>{SEATS.expires(new Date(d.expires_at).toLocaleString())}</span>}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2 border-t border-border-subtle pt-2 text-label text-fg-muted">
        {seat.live ? (
          <>
            <Link to={`/agents/${encodeURIComponent(seat.live.session_id)}`} className="underline-offset-2 hover:underline">{SEATS.session(seat.live.state)}</Link>
            {seat.live.needs_you && <Chip tone="warning">{SEATS.needsYou(seat.live.needs_you)}</Chip>}
            {seat.live.last_activity_at && <span>· {ageText(now - seat.live.last_activity_at)}</span>}
          </>
        ) : (
          <span>{SEATS.noSession}</span>
        )}
        {d && <span className="ml-auto" title={d.set_by}>{SEATS.declaredAgo(ageText(now - Date.parse(d.at)))}</span>}
      </div>
    </li>
  )
}

/** The Seats tab: one card per seat, what needs someone first. Seat events arrive on the bus, so it refreshes with it. */
export function SeatsTab({ busTick, now }: { busTick: number; now: number }) {
  const { data, error, reload } = useAgentsRead<{ seats: Seat[] }>('/seats')
  useEffect(() => {
    if (busTick) void reload()
  }, [busTick, reload])
  if (error && !data) return <p role="alert" className="p-4 text-xs text-danger">{SEATS.loadFailed}: {error}</p>
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="flex max-w-[1100px] flex-col gap-3 p-4">
        <p className="text-label text-fg-muted">{SEATS.intro}</p>
        {data === null ? (
          <div aria-busy className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-28 w-full" />)}</div>
        ) : data.seats.length === 0 ? (
          <p className="text-xs text-fg-muted">{SEATS.empty}</p>
        ) : (
          <ul aria-label={SEATS.label} className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {data.seats.map((s) => <SeatCard key={s.agent_urn} seat={s} now={now} />)}
          </ul>
        )}
      </div>
    </div>
  )
}
