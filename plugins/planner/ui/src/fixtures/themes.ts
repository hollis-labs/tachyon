/**
 * SAMPLE DATA. The backend does not serve links, a timeline
 * feed or momentum yet (build pack section 7), so the Themes lens reads these
 * until it does. Real: the theme list, progress, status actions and comments.
 */
import type { TimelineEvent } from '@planner/shared'

export const fixtureEvents: TimelineEvent[] = [
  { id: 'e1', day: 'Today', time: '10:12', facet: 'tasks', body: 'Wire the status menu moved to review', delta: 'review +1', momentum: { glyph: '↗', label: 'Raises momentum' } },
  { id: 'e2', day: 'Today', time: '09:40', facet: 'blockers', tone: 'blocked', body: 'Backend: link write with kind is blocked', delta: 'blocked +1', momentum: { glyph: '↘', label: 'Lowers momentum' } },
  { id: 'e3', day: 'Yesterday', time: '16:05', facet: 'prs', body: 'PR 160 merged', delta: '+1 done', momentum: { glyph: '↗', label: 'Raises momentum' }, card: 'Brief and fixtures for the Themes lens' },
  { id: 'e4', day: 'Yesterday', time: '11:30', facet: 'decisions', body: 'Decision: drag and drop stays on dnd-kit' },
]

export const fixtureMilestones = [
  { id: 'approved', label: 'Approved', state: 'reached' },
  { id: 'first', label: 'First task done', state: 'reached' },
  { id: 'half', label: '50% of tasks', state: 'next' },
  { id: 'tag', label: 'v1 tag', state: 'pending' },
] as const

export const fixtureMomentum = { label: 'Steady', detail: '1 done and 1 PR merged in 24h, 1 blocker weighs against' }

export const fixtureLinks = [
  { kind: 'needs', label: 'Backend link write' },
  { kind: 'delivers', label: 'Roadmap: Now' },
]
