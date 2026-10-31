import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, EmptyState, Skeleton } from '@hollis-labs/design-components'
import { FilterChipGroup } from '@hollis-labs/kit-dashboard/data'
import { DashboardSearchToolbar } from '@hollis-labs/kit-dashboard/layout'
import { CAPTION, Chip } from '@planner/shared'
import { RunOperations } from '../agents/TeamOperations'
import { Btn } from '../agents/ui'
import { isRetired, ownedBuilderClient, type ObjectRecord } from '../agents/owned-builder-client'
import { providerActionsClient as provider, type ActionSummary, type RosterSnapshot } from '../agents/provider-actions'
import { useOwnedRecords } from '../agents/use-owned-records'
import { Avatar } from './session-rows'
import { TEAMS } from './vocabulary'

type RunState = 'running' | 'idle' | 'unknown'
type Summary = { load: 'loading' | 'ready' | 'error'; error?: string; formations: ActionSummary[]; latest?: ActionSummary; roster?: RosterSnapshot }
type Filter = keyof typeof TEAMS.filters

const slotsOf = (t: ObjectRecord): { name: string }[] => {
  const s = (t.definition as { slots?: unknown }).slots
  return Array.isArray(s) ? s.filter((x): x is { name: string } => !!x && typeof (x as { name?: unknown }).name === 'string') : []
}
const runOf = (s: Summary | undefined): RunState => {
  if (!s || s.load === 'loading') return 'unknown'
  if (s.load === 'error') return 'unknown'
  if (!s.latest) return 'idle'
  const st = s.roster?.runs[0]?.run.status
  return st ? (st === 'active' || st === 'running' ? 'running' : 'idle') : 'unknown'
}
const when = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '')

/** Load the latest confirmed formation and its saved roster for a team: two cheap reads, no refresh against Tether. */
async function readSummary(id: string): Promise<Summary> {
  try {
    const page = await provider.list(id, '', 'teams.form')
    const formations = page.items.filter((f) => f.state === 'succeeded')
    const latest = formations[0]
    if (!latest) return { load: 'ready', formations }
    const roster = await provider.roster(latest.id).catch(() => undefined)
    return { load: 'ready', formations, latest, roster }
  } catch (e) {
    return { load: 'error', error: e instanceof Error ? e.message : TEAMS.unknownRunReason, formations: [] }
  }
}

function RunDot({ run }: { run: RunState }) {
  const tone = run === 'running' ? 'bg-fg/90' : run === 'idle' ? 'bg-fg-faint/60' : 'border border-fg-muted'
  return <i aria-hidden className={`inline-block size-1.5 shrink-0 rounded-full ${tone}`} />
}

function TeamRow({ team, summary, onOpen }: { team: ObjectRecord; summary: Summary | undefined; onOpen: () => void }) {
  const run = runOf(summary)
  const slots = slotsOf(team)
  const members = summary?.roster?.runs[0]?.members ?? []
  const status = run === 'running' ? summary?.roster?.runs[0]?.run.status ?? 'running' : run === 'idle' ? TEAMS.notFormed : TEAMS.unknownRun
  return (
    <li>
      <button type="button" onClick={onOpen} className="group flex w-full items-center gap-3 px-4 py-3 text-left outline-none hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
        <Avatar name={team.name} />
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex min-w-0 flex-wrap items-center gap-1.5">
            <b className="truncate text-sm font-semibold text-fg">{team.name}</b>
            <Chip square>{slots.length ? TEAMS.slots(slots.length) : TEAMS.noSlots}</Chip>
            {team.ownership !== 'tachyon' && <Chip square>{TEAMS.imported}</Chip>}
            {isRetired(team) && <Chip tone="warning">{TEAMS.retired}</Chip>}
          </span>
          <span className="flex min-w-0 items-center gap-2 text-xs text-fg-secondary" title={summary?.load === 'error' ? summary.error : undefined}>
            <RunDot run={run} />
            <span className="truncate">{status}</span>
            {run === 'running' && <span className="text-fg-muted">· {TEAMS.members(members.length)}</span>}
          </span>
        </span>
        <span className="hidden shrink-0 text-right font-mono text-label text-fg-muted sm:block">
          {summary?.latest ? TEAMS.formedAt(when(summary.latest.created_at)) : ''}
        </span>
      </button>
    </li>
  )
}

function TeamDetail({ team, summary, onClose }: { team: ObjectRecord; summary: Summary | undefined; onClose: () => void }) {
  const [locked, setLocked] = useState(false)
  const slots = slotsOf(team)
  const run = runOf(summary)
  const roster = summary?.roster?.runs[0]
  const members = roster?.members ?? []
  return (
    <Dialog open onOpenChange={(o) => !o && !locked && onClose()}>
      <DialogContent showCloseButton={false} showFullscreenToggle={false} widthClassName="max-w-[860px]" className="flex h-[min(680px,calc(100dvh-5rem))] flex-col gap-0 overflow-hidden p-0">
        <header className="flex flex-wrap items-center gap-x-2.5 gap-y-2 border-b border-border-subtle px-4 py-3">
          <Avatar name={team.name} />
          <div className="flex min-w-0 flex-col">
            <DialogTitle className="font-sans text-[15px] font-semibold normal-case leading-tight tracking-normal text-fg">{team.name}</DialogTitle>
            <DialogDescription className="text-label text-fg-muted">{TEAMS.slots(slots.length)} · {team.id}</DialogDescription>
          </div>
          <span className="flex items-center gap-2 text-xs text-fg"><RunDot run={run} />{run === 'running' ? roster?.run.status : run === 'idle' ? TEAMS.notFormed : TEAMS.unknownRun}</span>
          <span className="flex-1" />
          <Link to="/builder/teams" className="text-xs text-fg-secondary underline-offset-2 hover:underline">{TEAMS.openBuilder}</Link>
          <DialogClose aria-label={TEAMS.close} className="grid size-7 place-items-center rounded-control border border-border text-fg-secondary hover:bg-surface-hover">✕</DialogClose>
        </header>
        <div tabIndex={0} className="flex min-h-0 flex-1 flex-col gap-5 overflow-auto p-4 outline-none focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
          {summary?.load === 'error' && <p role="alert" className="text-xs text-warning">{summary.error}</p>}
          {summary?.roster?.problem && <p role="alert" className="text-xs text-warning">{summary.roster.problem}</p>}
          <section aria-labelledby="team-members" className="flex flex-col gap-1.5">
            <h2 id="team-members" className={CAPTION}>{TEAMS.members_h}{members.length ? ` · ${members.length}` : ''}</h2>
            <p className="text-label text-fg-muted">{summary?.roster?.observed_at ? TEAMS.rosterSeen(when(summary.roster.observed_at)) : TEAMS.noRoster}</p>
            {members.length === 0 ? <p className="text-xs text-fg-muted">{TEAMS.noMembers}</p> : (
              <table className="w-full border-collapse text-xs">
                <thead><tr className="text-left"><th className={`${CAPTION} py-1 font-normal`}>{TEAMS.colSlot}</th><th className={`${CAPTION} py-1 font-normal`}>{TEAMS.colMember}</th><th className={`${CAPTION} py-1 font-normal`}>{TEAMS.colStatus}</th><th className={`${CAPTION} hidden py-1 font-normal sm:table-cell`}>{TEAMS.colActor}</th></tr></thead>
                <tbody>{members.map((m) => (
                  <tr key={m.id} className="border-t border-border-subtle">
                    <td className="py-1.5 font-medium text-fg">{m.slot}</td>
                    <td className="py-1.5 font-mono text-label text-fg-secondary">{m.id}</td>
                    <td className="py-1.5 text-fg-secondary">{m.status}</td>
                    <td className="hidden py-1.5 text-fg-secondary sm:table-cell">{m.actor}</td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </section>
          <section aria-labelledby="team-slots" className="flex flex-col gap-1.5">
            <h2 id="team-slots" className={CAPTION}>{TEAMS.slots_h}</h2>
            <div className="flex flex-wrap gap-1.5">{slots.length ? slots.map((s) => <Chip key={s.name} square>{s.name}</Chip>) : <p className="text-xs text-fg-muted">{TEAMS.noSlots}</p>}</div>
          </section>
          <section aria-labelledby="team-formations" className="flex flex-col gap-1.5">
            <h2 id="team-formations" className={CAPTION}>{TEAMS.formations_h}</h2>
            {summary?.formations.length ? (
              <ul className="flex flex-col text-xs text-fg-secondary">{summary.formations.slice(0, 5).map((f) => <li key={f.id} className="flex justify-between gap-3 border-t border-border-subtle py-1.5"><span className="font-mono text-label">{f.id}</span><span className="text-fg-muted">{when(f.created_at)}</span></li>)}</ul>
            ) : <p className="text-xs text-fg-muted">{TEAMS.noFormations}</p>}
          </section>
          {summary?.latest && (
            <section aria-labelledby="team-manage" className="flex flex-col gap-1.5">
              <h2 id="team-manage" className={CAPTION}>{TEAMS.manage_h}</h2>
              <p className="text-label text-fg-muted">{TEAMS.manageHint}</p>
              <div className="flex flex-col gap-3 rounded-panel border border-border bg-bg-elevated p-3">
                <RunOperations key={summary.latest.id} team={team} formation={{ id: summary.latest.id, object_id: team.id, object_revision: summary.latest.object_revision, target: '', verb: 'teams.form', payload: {}, digest: '', state: 'succeeded', attempts: 0, result: {}, created_at: summary.latest.created_at, updated_at: summary.latest.created_at }} onLocked={setLocked} />
              </div>
            </section>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function TeamsTab() {
  const teams = useOwnedRecords(ownedBuilderClient, 'team')
  const [summaries, setSummaries] = useState<Record<string, Summary>>({})
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [open, setOpen] = useState<string | null>(null)
  const started = useRef(new Set<string>())
  const load = (id: string) => {
    setSummaries((s) => ({ ...s, [id]: { ...(s[id] ?? { formations: [] }), load: 'loading' } as Summary }))
    void readSummary(id).then((r) => setSummaries((s) => ({ ...s, [id]: r })))
  }
  useEffect(() => {
    for (const t of teams.rows.slice(0, 24)) if (!started.current.has(t.id)) { started.current.add(t.id); load(t.id) }
  }, [teams.rows])
  const rows = teams.rows.filter((t) => !isRetired(t) || summaries[t.id]?.latest)
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return rows.filter((t) => {
      const r = runOf(summaries[t.id])
      if (filter === 'running' && r !== 'running') return false
      if (filter === 'idle' && r !== 'idle') return false
      if (!q) return true
      const members = summaries[t.id]?.roster?.runs[0]?.members ?? []
      return [t.name, t.id, ...slotsOf(t).map((s) => s.name), ...members.flatMap((m) => [m.id, m.slot, m.actor])].some((x) => x.toLowerCase().includes(q))
    })
  }, [rows, summaries, query, filter])
  const count = (f: Filter) => (f === 'all' ? rows.length : rows.filter((t) => runOf(summaries[t.id]) === (f === 'running' ? 'running' : 'idle')).length)
  const groups = (['running', 'idle', 'unknown'] as const).map((g) => ({ g, list: visible.filter((t) => runOf(summaries[t.id]) === g) })).filter((x) => x.list.length)
  const selected = open ? teams.rows.find((t) => t.id === open) : undefined
  return (
    <section aria-label={TEAMS.listLabel} className="flex min-h-0 flex-1 flex-col">
      <DashboardSearchToolbar value={query} onChange={setQuery} label={TEAMS.searchLabel} placeholder={TEAMS.searchPlaceholder} slashToFocus>
        <FilterChipGroup ariaLabel={TEAMS.filtersLabel} variant="outline" chips={(Object.keys(TEAMS.filters) as Filter[]).map((f) => ({ value: f, label: f === 'all' ? TEAMS.filters.all : `${TEAMS.filters[f]} ${count(f)}` }))} selected={[filter]} onToggle={(v) => setFilter(v as Filter)} />
      </DashboardSearchToolbar>
      <div tabIndex={0} className="min-h-0 flex-1 overflow-auto outline-none focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
        {teams.error && teams.rows.length === 0 ? (
          <EmptyState variant="error" title={TEAMS.loadFailed} description={teams.error} action={{ label: TEAMS.retry, onClick: () => void teams.refresh() }} />
        ) : teams.loading && teams.rows.length === 0 ? (
          <div className="flex flex-col gap-2 p-4" aria-busy>{[0, 1, 2].map((i) => <Skeleton key={i} className="h-14 w-full" />)}</div>
        ) : rows.length === 0 ? (
          <EmptyState variant="empty" title={TEAMS.emptyTitle} description={TEAMS.emptyBody} action={{ label: TEAMS.openBuilder, onClick: () => { window.location.assign('/builder/teams') } }} />
        ) : visible.length === 0 ? (
          <EmptyState variant="empty" title={TEAMS.noMatch} description="" action={{ label: TEAMS.clear, onClick: () => { setQuery(''); setFilter('all') } }} />
        ) : groups.map(({ g, list }) => (
          <div key={g}>
            <h2 className={`flex items-center gap-2.5 px-4 pb-1.5 pt-3.5 ${CAPTION}`}><span>{TEAMS.groups[g]}</span><span className="text-fg-faint">{list.length}</span></h2>
            <ul className="divide-y divide-border-subtle border-y border-border-subtle">{list.map((t) => <TeamRow key={t.id} team={t} summary={summaries[t.id]} onOpen={() => { setOpen(t.id); if (!summaries[t.id]) load(t.id) }} />)}</ul>
          </div>
        ))}
        {teams.next && <div className="p-4"><Btn disabled={teams.loading} onClick={() => void teams.more()}>More teams</Btn></div>}
      </div>
      {selected && <TeamDetail team={selected} summary={summaries[selected.id]} onClose={() => setOpen(null)} />}
    </section>
  )
}
