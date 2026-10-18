import { RankBadge, RecordTag } from '@hollis-labs/kit-dashboard/data'
import { statusTone } from '@hollis-labs/kit-dashboard'
import type { PlannerDb } from '@planner/api/types'

/** Reserve stronger accents for rank, severity and delivery milestones. */
export function PlannerFact({ db, fact }: { db: PlannerDb; fact: string }) {
  const value = fact.toLowerCase()
  // Rank is a priority badge, not an outlined metadata tag. Keep its #rank label.
  if (db === 'priorities') return <RankBadge>{fact}</RankBadge>
  const tone = db === 'risks' && ['critical', 'high'].includes(value) ? 'blocked'
    : db === 'risks' && value === 'medium' ? 'queued'
    : value === 'mvp' ? 'done'
    : db === 'roadmap' && value === 'now' ? 'doing'
    : db === 'roadmap' && value === 'next' ? 'queued'
    : null
  const colors = tone ? statusTone(tone) : null
  return <RecordTag title={fact} className={colors ? `${colors.border} ${colors.bg} ${colors.text}` : undefined}>{fact}</RecordTag>
}
