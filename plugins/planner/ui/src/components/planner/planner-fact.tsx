import { statusTone } from '@hollis-labs/kit-dashboard'
import type { PlannerDb } from '@tachyon/torque-client'

/** Reserve stronger accents for rank, severity and delivery milestones. */
export function PlannerFact({ db, fact }: { db: PlannerDb; fact: string }) {
  const value = fact.toLowerCase()
  // Rank is a priority badge, not an outlined metadata tag. Keep its #rank label.
  if (db === 'priorities') return <span className="planner-priority" title={`Priority rank ${fact.replace(/^#/, '')}`}><span>{fact}</span></span>
  const tone = db === 'risks' && ['critical', 'high'].includes(value) ? 'blocked'
    : db === 'risks' && value === 'medium' ? 'queued'
    : value === 'mvp' ? 'done'
    : db === 'roadmap' && value === 'now' ? 'doing'
    : db === 'roadmap' && value === 'next' ? 'queued'
    : null
  const colors = tone ? statusTone(tone) : null
  return <span title={fact} className={`planner-fact max-w-full rounded border px-1.5 py-px text-caption leading-4 [overflow-wrap:anywhere] ${colors ? `${colors.border} ${colors.bg} ${colors.text}` : 'border-border-subtle bg-panel-2/30 text-fg-faint'}`}>{fact}</span>
}
