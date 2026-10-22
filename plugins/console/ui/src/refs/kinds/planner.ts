import type { RefKind } from '../types'
import { isWordChar, matches } from '../scan-utils'

interface PlannerEntity {
  id: string
  label: string
  icon: string
  /** Matches one id; the whole match is the canonical id. */
  pattern: RegExp
}

// Portfolio planner ids as they appear in chat: themes PR-07, decisions DEC-052,
// risks RK-12, inbox IN-88, workstreams WS-name. PR-nn is two digits only, so a
// pull request written PR-113 is left alone.
export const PLANNER_ENTITIES: PlannerEntity[] = [
  { id: 'planner.theme', label: 'Theme', icon: 'plan', pattern: /PR-\d{2}/g },
  { id: 'planner.decision', label: 'Decision', icon: 'plan', pattern: /DEC-\d{1,4}/g },
  { id: 'planner.risk', label: 'Risk', icon: 'plan', pattern: /RK-\d{1,4}/g },
  { id: 'planner.inbox', label: 'Inbox item', icon: 'plan', pattern: /IN-\d{2,4}/g },
  { id: 'planner.workstream', label: 'Workstream', icon: 'workstream', pattern: /WS-[A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+){0,6}/g },
]

function standalone(text: string, start: number, end: number): boolean {
  const before = text[start - 1]
  if (isWordChar(before) || before === '-' || before === '/' || before === '\\' || before === '.' || before === '#') return false
  const after = text[end]
  if (isWordChar(after) || after === '/' || after === '\\' || after === '-') return false
  if (after === '.' && isWordChar(text[end + 1])) return false
  return true
}

/** One kind per planner database so each carries its own label and resolver outcome. */
export function createPlannerKinds(): RefKind[] {
  return PLANNER_ENTITIES.map((e) => ({
    id: e.id,
    label: e.label,
    icon: e.icon,
    priority: 75,
    scan(text) {
      const hits = []
      for (const m of matches(e.pattern, text)) {
        const start = m.index
        const end = start + m[0].length
        if (standalone(text, start, end)) hits.push({ start, end, canonical: m[0] })
      }
      return hits
    },
    // Opens in the in-place drawer; the planner has no per-item page to link to.
    open: () => ({ type: 'none' }),
  }))
}
