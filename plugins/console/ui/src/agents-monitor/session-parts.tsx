import { cn } from '@hollis-labs/design-components'
import { AGENTS } from './vocabulary'
import type { OneLinerData, Session } from './types'

/** "5s", "3m", "2h", "5d": the largest whole unit. */
export function ageText(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

/** Verb, target as a code chip and age, cut with an ellipsis; the full text is the title. */
export function OneLiner({ line, now, fallback }: { line: OneLinerData | null; now: number; fallback: string }) {
  if (!line) return <span className="truncate dash-support text-fg-faint">{fallback}</span>
  const age = ageText(now - line.at)
  return (
    <span className="min-w-0 truncate dash-support text-fg-muted" title={`${line.verb} ${line.target} ${age}`}>
      {line.verb} <code className="rounded-sm bg-surface-hover px-1 font-mono dash-caption text-fg">{line.target}</code> <span className="text-fg-faint">{age}</span>
    </span>
  )
}

/** Context use as a bar and a number. Unknown stays unknown (never a zero bar); danger tone from 90%. */
export function ContextMeter({ pct, className }: { pct: number | null; className?: string }) {
  if (pct === null) return <span className={cn('dash-caption text-fg-faint', className)}>{AGENTS.contextUnknown}</span>
  const clamped = Math.max(0, Math.min(100, pct))
  return (
    <span className={cn('flex items-center gap-2', className)}>
      <span role="progressbar" aria-label={AGENTS.contextLabel(pct)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={clamped} className="h-1.5 w-20 overflow-hidden rounded-full bg-surface-hover">
        <span className={cn('block h-full', clamped >= 90 ? 'bg-danger' : 'bg-fg opacity-60')} style={{ width: `${clamped}%` }} />
      </span>
      <span className="font-mono dash-caption tabular-nums text-fg-muted">{clamped}%</span>
    </span>
  )
}

/** Per-minute activity over the last 30 minutes. Decorative: the one-liner and state carry the meaning. */
export function Sparkline({ points, className }: { points: readonly number[]; className?: string }) {
  const max = Math.max(1, ...points)
  const w = 60
  const h = 14
  const step = points.length > 1 ? w / (points.length - 1) : w
  const d = points.map((v, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)} ${(h - (v / max) * h).toFixed(1)}`).join(' ')
  return (
    <svg aria-hidden viewBox={`0 0 ${w} ${h}`} className={cn('h-3.5 w-16 text-fg-faint', className)} fill="none" stroke="currentColor" strokeWidth="1">
      <path d={d} />
    </svg>
  )
}

/** State as words, never colour alone; a stale session says why, an idle one says for how long. */
export function stateText(s: Session, now: number): string {
  if (s.needs_you.reason) return AGENTS.needsYou[s.needs_you.reason]
  if (s.state === 'stale') return s.stale_reason ? `${AGENTS.states.stale}: ${AGENTS.staleReasons[s.stale_reason]}` : AGENTS.states.stale
  if (s.state === 'idle' && s.last_activity_at !== null) return AGENTS.idleFor(ageText(now - s.last_activity_at))
  if (s.state === 'working' && s.state_inferred) return `~${AGENTS.states.working.toLowerCase()} (${AGENTS.inferred})`
  return AGENTS.states[s.state]
}
