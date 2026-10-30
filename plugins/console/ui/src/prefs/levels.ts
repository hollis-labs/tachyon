import { severityAtLeast } from '../alerts/policy'
import type { ConsolePrefs } from './types'

/** The single "Notify me about" choice that replaces the separate bell/list and toast thresholds. */
export type NotifyLevel = 'blockers' | 'warnings' | 'everything'

export const NOTIFY_LEVELS: { id: NotifyLevel; label: string; patch: Pick<ConsolePrefs, 'bellMinSeverity' | 'toastMinSeverity'> }[] = [
  { id: 'blockers', label: 'Blockers', patch: { bellMinSeverity: 'error', toastMinSeverity: 'error' } },
  { id: 'warnings', label: 'Warnings', patch: { bellMinSeverity: 'warning', toastMinSeverity: 'warning' } },
  // Everything lists every alert; pop-ups still start at notice so debug noise never interrupts.
  { id: 'everything', label: 'Everything', patch: { bellMinSeverity: 'debug', toastMinSeverity: 'notice' } },
]

/** Maps stored prefs to a level: error+ is blockers, warning+ is warnings, anything lower is everything. */
export function levelOf(p: Pick<ConsolePrefs, 'bellMinSeverity'>): NotifyLevel {
  if (severityAtLeast(p.bellMinSeverity, 'error')) return 'blockers'
  if (severityAtLeast(p.bellMinSeverity, 'warning')) return 'warnings'
  return 'everything'
}

export const levelPatch = (level: NotifyLevel) => NOTIFY_LEVELS.find((l) => l.id === level)!.patch

export function previewLine(level: NotifyLevel, quiet: boolean): string {
  const what = level === 'blockers' ? 'Errors and critical alerts show' : level === 'warnings' ? 'Warnings, errors and critical alerts show' : 'Every alert shows'
  const popups = level === 'everything' ? 'Pop-ups start at notices.' : 'They also appear as pop-ups.'
  return `${what} in the bell and list. ${quiet ? 'Quiet is on: only critical alerts pop up.' : popups}`
}
