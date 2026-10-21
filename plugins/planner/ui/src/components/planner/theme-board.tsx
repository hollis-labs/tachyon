import { Link } from 'react-router-dom'
import { statusTone } from '@hollis-labs/kit-dashboard'
import { PlannerRowContent, PlannerTableHeader } from '@planner/components/planner/planner-table'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import { THEME_SECTIONS, boardAge, boardMore, themeOwners, themeProgressFraction, themeProgressText, themeWaiting, verifiedAge } from '@planner/lib/planner'
import type { ThemeBoard, ThemeRow, ThemeSectionKey } from '@planner/api/types'

interface ThemeBoardViewProps {
  board: ThemeBoard
  /** The moment ages are measured from; the board's own generation time by default. */
  nowMs?: number
  /** Where rows link: the board path the view lives at. */
  base?: string
  /** Proposed themes are hidden until asked for. */
  showProposed?: boolean
}

/**
 * The flight board: themes the operator has approved and sequenced, what is
 * ready behind them, and what is parked. One row per theme, with how much of its
 * work is done. The words are display only; the stored status is task-semantic.
 */
export function ThemeBoardView({ board, nowMs, base = '/planner/flight', showProposed = false }: ThemeBoardViewProps) {
  const now = nowMs ?? (Date.parse(board.generated_at) || Date.now())
  const sections = THEME_SECTIONS.filter((s) => s.key !== 'proposed' || showProposed)
  return (
    <div className="flex min-w-0 flex-col">
      {board.notices.map((n) => (
        <p key={n} role="status" className="border-b border-border bg-panel px-4 py-2 dash-support leading-5 text-fg-muted">
          {n}
        </p>
      ))}
      <PlannerTableHeader trailing="Progress" />
      <div className="flex min-w-0 flex-col">
        {sections.map((s) => (
          <ThemeSection key={s.key} section={s.key} title={s.title} blurb={s.blurb} empty={s.empty} rows={board.sections[s.key] ?? []} total={board.totals[s.key] ?? 0} now={now} base={base} />
        ))}
      </div>
      <p className="px-4 py-2 font-mono dash-caption leading-4 text-fg-faint">
        Updated {new Date(board.generated_at).toLocaleTimeString()} · {board.progress.done}/{board.progress.total} tasks done across the themes shown · landed covers the last {Math.round(board.recent_hours / 24)} days
      </p>
    </div>
  )
}

function ThemeSection({ section, title, blurb, empty, rows, total, now, base }: { section: ThemeSectionKey; title: string; blurb: string; empty: string; rows: ThemeRow[]; total: number; now: number; base: string }) {
  const more = boardMore(rows.length, total)
  const accent = statusTone(THEME_SECTIONS.find((s) => s.key === section)!.tone)
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
          {rows.map((r, i) => (
            <ThemeRowView key={r.id} section={section} row={r} position={section === 'in_flight' || section === 'cleared' ? i + 1 : undefined} now={now} base={base} />
          ))}
        </ul>
      )}
    </section>
  )
}

function ThemeRowView({ section, row, position, now, base }: { section: ThemeSectionKey; row: ThemeRow; position?: number; now: number; base: string }) {
  const def = THEME_SECTIONS.find((s) => s.key === section)!
  const verified = verifiedAge(row.last_verified, now)
  const waiting = themeWaiting(row)
  const summary = [
    row.blocked_count > 0 ? `Blocked${row.blocked_count > 1 ? ` (${row.blocked_count})` : ''}: ${row.first_blocker ?? ''}` : '',
    row.next_step ? `Next: ${row.next_step}` : '',
  ].filter(Boolean).join(' · ')
  const details = [
    ...waiting,
    themeOwners(row),
    section === 'parked' && row.idle_tasks ? `${row.idle_tasks} tasks idle` : '',
    verified.label,
  ].filter(Boolean).join(' · ')
  const fraction = themeProgressFraction(row.progress)
  const pct = fraction === null ? null : Math.round(fraction * 100)
  const tone = statusTone(def.tone)
  return (
    <li className="dash-record-list-row">
      <Link to={`${base}?open=themes/${encodeURIComponent(row.id)}`} className="planner-row-trigger dash-record-grid" aria-label={`${def.word} ${row.title}${position ? `, position ${position}` : ''}`}>
        <PlannerRowContent
          title={position ? `${position}. ${row.title}` : row.title}
          metadata={<>
            <span className="shrink-0">{row.id.slice(0, 8)}</span>
            <span className={`min-w-0 overflow-hidden text-ellipsis ${row.blocked_count > 0 ? 'text-warning' : verified.stale ? 'text-fg-faint' : ''}`} title={[summary, details].filter(Boolean).join(' · ')}>
              {[summary, details].filter(Boolean).join(' · ')}
            </span>
          </>}
          status={<PlannerStatus status={def.word} toneKey={def.tone} />}
          trailing={
            <span className="inline-flex min-w-28 flex-col items-end gap-1" title={`${themeProgressText(row.progress)} · updated ${boardAge(row.updated, now)} ago`}>
              <span className="font-mono dash-caption tabular-nums">{themeProgressText(row.progress)}</span>
              {pct !== null && (
                <span role="progressbar" aria-label={`${row.title} progress`} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} className="block h-1 w-28 rounded-full bg-border">
                  <span className={`block h-1 rounded-full ${tone.dot}`} style={{ width: `${pct}%` }} />
                </span>
              )}
            </span>
          }
        />
      </Link>
    </li>
  )
}
