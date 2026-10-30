import { useState } from 'react'
import { Switch } from '@hollis-labs/design-components'
import { Segmented } from '../components/segmented'
import { levelOf, levelPatch, NOTIFY_LEVELS, previewLine } from './levels'
import { PREF_FIELD_KEYS, consolePrefsSettingsGroup } from './manifest'
import type { ConsolePrefs } from './types'

const LABEL = 'text-sm font-medium text-fg'
const INPUT =
  'border border-border bg-bg px-1.5 py-1 text-sm text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50'
const HELP = 'mt-1 text-xs leading-snug text-fg-muted'

export interface ConsolePrefsPanelProps {
  prefs: ConsolePrefs
  onChange: (patch: Partial<ConsolePrefs>) => void
}

/**
 * In-app preferences form, built on plain controls. Field titles and help text
 * come from the manifest group (consolePrefsSettingsGroup), so swapping this for
 * kit-settings' <SettingsRenderer> changes the chrome, not the data. Edits apply
 * immediately (no draft/save step) because the store is local; see BACKEND_NEEDS.md.
 */
export function ConsolePrefsPanel({ prefs, onChange }: ConsolePrefsPanelProps) {
  const props = consolePrefsSettingsGroup().schema.properties
  const help = (field: keyof typeof PREF_FIELD_KEYS) => String(props[field]?.description ?? '')
  // Muted sources edit as free text; commit on blur so typing a comma is not eaten by normalising.
  const [mutedDraft, setMuted] = useState<string | null>(null)
  const muted = mutedDraft ?? prefs.mutedSources.join(', ')

  return (
    <form aria-label="Alert preferences" className="space-y-5" onSubmit={(e) => e.preventDefault()}>
      <div>
        <div className="flex items-center gap-3">
          <Switch aria-label="Quiet mode" checked={prefs.quiet} onCheckedChange={(v) => onChange({ quiet: v })} />
          <span className={LABEL} aria-hidden="true">Quiet mode</span>
        </div>
        <p className="mt-1 text-xs leading-snug text-fg-muted">{help('quiet')}</p>
      </div>

      <div>
        <p className={LABEL} id="notify-level">Notify me about</p>
        <Segmented
          label="Notify me about"
          value={levelOf(prefs)}
          onChange={(id) => onChange(levelPatch(id))}
          className="!border-b-0 !px-0"
          items={NOTIFY_LEVELS.map((l) => ({ value: l.id, label: l.label }))}
        />
        <p className={HELP} aria-live="polite">{previewLine(levelOf(prefs), prefs.quiet)}</p>
      </div>

      <fieldset className="space-y-1.5">
        <legend className="sr-only">Quiet hours</legend>
        <div className="flex items-center gap-3">
          <Switch aria-label="Quiet hours" checked={prefs.quietHoursEnabled} onCheckedChange={(v) => onChange({ quietHoursEnabled: v })} />
          <span className={LABEL} aria-hidden="true">Quiet hours</span>
        </div>
        <div className="flex items-center gap-2">
          <input type="time" aria-label="Quiet hours start" value={prefs.quietHoursStart} disabled={!prefs.quietHoursEnabled} onChange={(e) => e.target.value && onChange({ quietHoursStart: e.target.value })} className={INPUT} />
          <span className={LABEL}>to</span>
          <input type="time" aria-label="Quiet hours end" value={prefs.quietHoursEnd} disabled={!prefs.quietHoursEnabled} onChange={(e) => e.target.value && onChange({ quietHoursEnd: e.target.value })} className={INPUT} />
        </div>
        <p className="text-xs leading-snug text-fg-muted">{help('quiet_hours_enabled')}</p>
      </fieldset>

      <div>
        <label className="block">
          <span className={LABEL}>Muted sources</span>
          <input
            type="text"
            value={muted}
            placeholder="agent-bravo, laptop"
            onChange={(e) => setMuted(e.target.value)}
            onBlur={() => {
              if (mutedDraft !== null) onChange({ mutedSources: mutedDraft.split(',') })
              setMuted(null)
            }}
            className={`${INPUT} mt-1 w-full`}
          />
        </label>
        <p className="mt-1 text-xs leading-snug text-fg-muted">{help('muted_sources')}</p>
      </div>

      <div>
        <label className="flex items-center gap-2">
          <span className={LABEL}>Coalesce window (s)</span>
          <input
            type="number"
            min={0}
            max={3600}
            value={prefs.coalesceWindowSec}
            onChange={(e) => onChange({ coalesceWindowSec: e.target.valueAsNumber })}
            className={`${INPUT} w-20`}
          />
        </label>
        <p className="mt-1 text-xs leading-snug text-fg-muted">{help('coalesce_window_sec')}</p>
      </div>
    </form>
  )
}
