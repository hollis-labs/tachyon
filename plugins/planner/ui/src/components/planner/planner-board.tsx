import { Link } from 'react-router-dom'
import { statusTone } from '@hollis-labs/kit-dashboard'
import { PlannerRowContent, PlannerTableHeader } from '@planner/components/planner/planner-table'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import { BOARD_SECTIONS, CURATED_SECTIONS, boardAge, boardMore, boardRowRoute, boardWord, taskCountsText } from '@planner/lib/planner'
import type { PlannerBoard, PlannerBoardRow, PlannerBoardSectionKey } from '@planner/api/types'

interface PlannerBoardViewProps {
  board: PlannerBoard
  /** The moment ages are measured from; the board's own generation time by default. */
  nowMs?: number
  /** The curated board of tagged workstreams, or the activity feed with Torque tasks. */
  scope?: 'curated' | 'all'
  /** Where rows link: the board path the view lives at. */
  base?: string
}

/** The board: four sections, one row per line. */
export function PlannerBoardView({ board, nowMs, scope = 'curated', base = '/planner/board' }: PlannerBoardViewProps) {
  const sections = scope === 'curated' ? CURATED_SECTIONS : BOARD_SECTIONS
  const now = nowMs ?? (Date.parse(board.generated_at) || Date.now())
  return (
    <div className="flex min-w-0 flex-col">
      {board.notices.map((n) => (
        <p key={n} role="status" className="border-b border-border bg-panel px-4 py-2 dash-support leading-5 text-fg-muted">
          {n}
        </p>
      ))}
      <PlannerTableHeader trailing="Age" />
      <div className="flex min-w-0 flex-col">
        {sections.map((s) => (
          <BoardSection key={s.key} section={s.key} title={s.title} blurb={s.blurb} empty={s.empty} rows={board.sections[s.key]} total={board.totals[s.key]} now={now} base={base} />
        ))}
      </div>
      <p className="px-4 py-2 font-mono dash-caption leading-4 text-fg-faint">
        Updated {new Date(board.generated_at).toLocaleTimeString()} · landed covers the last {board.recent_hours}h
        {scope === 'all' ? <> · a task with no update or comment for {board.active_hours}h counts as preparing, not in flight</> : <> · tag a workstream with board to put it here</>}
      </p>
    </div>
  )
}

function BoardSection({ section, title, blurb, empty, rows, total, now, base }: { section: PlannerBoardSectionKey; title: string; blurb: string; empty: string; rows: PlannerBoardRow[]; total: number; now: number; base: string }) {
  const more = boardMore(rows.length, total)
  const accent = statusTone(({ in_flight: 'doing', pre_flight: 'queued', landed: 'done', holds: 'blocked' } as const)[section])
  return (
    <section aria-label={title} className="flex min-w-0 flex-col">
      <header className="dash-record-section-header">
        <h2 className={`inline-flex items-center gap-2 font-mono dash-caption font-medium uppercase tracking-label ${accent.text}`}><span className={`size-1.5 rounded-full ${accent.dot}`} />{title}</h2>
        <span className={`font-mono dash-caption tabular-nums ${accent.text}`}>{rows.length}</span>
        {more && <span className="dash-caption text-fg-faint">({more})</span>}
        <span className="dash-label text-fg-faint">{blurb}</span>
      </header>
      {rows.length === 0 ? (
        <p className="px-4 py-3 dash-support text-fg-muted">{empty}</p>
      ) : (
        <ul>
          {rows.map((r) => (
            <BoardRow key={`${r.source}/${r.id}`} section={section} row={r} now={now} base={base} />
          ))}
        </ul>
      )}
    </section>
  )
}

function BoardRow({ section, row, now, base }: { section: PlannerBoardSectionKey; row: PlannerBoardRow; now: number; base: string }) {
  const { word, tone } = boardWord(section, row)
  const age = boardAge(row.updated, now)
  const taskCounts = taskCountsText(row.task_counts)
  const summary = [
    ...(row.blockers ?? []).map(b => `Blocked: ${b}`),
    row.next_step ? `Next: ${row.next_step}` : '',
  ].filter(Boolean).join(' · ')
  const details = [row.workstream, row.owner, row.phase !== 'unknown' ? row.phase : '', taskCounts ? `tasks: ${taskCounts}` : ''].filter(Boolean).join(' · ')
  return (
    <li className="dash-record-list-row">
      <Link to={boardRowRoute(row, base)} className="planner-row-trigger dash-record-grid" aria-label={`${word} ${row.id}: ${row.title}`}>
        <PlannerRowContent title={row.title} metadata={<>
          <span className="shrink-0">{row.id}</span>
          <span className={`min-w-0 overflow-hidden text-ellipsis ${row.blockers?.length ? 'text-warning' : ''}`} title={[summary, details].filter(Boolean).join(' · ')}>
            {[summary, details].filter(Boolean).join(' · ')}
          </span>
        </>} status={<PlannerStatus status={word} toneKey={tone} />} trailing={<span title={row.updated ? new Date(row.updated).toLocaleString() : undefined}>{age}</span>} />
      </Link>
    </li>
  )
}
