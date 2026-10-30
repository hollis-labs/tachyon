import { useState, type ReactNode } from 'react'
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Switch } from '@hollis-labs/design-components'
import { X } from 'lucide-react'
import { Chip } from '../components/chip'
import { Segmented } from '../components/segmented'
import { levelOf, levelPatch, NOTIFY_LEVELS, previewLine, seeLine } from './levels'
import { consolePrefsSettingsGroup } from './manifest'
import type { ConsolePrefs } from './types'

const INPUT =
  'border border-border bg-bg px-1.5 py-1 dash-control text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50'
const WINDOWS = [[0, 'Off'], [30, '30 seconds'], [120, '2 minutes'], [600, '10 minutes']] as const

export interface ConsolePrefsPanelProps {
  prefs: ConsolePrefs
  onChange: (patch: Partial<ConsolePrefs>) => void
}

function Row({ title, help, children }: { title: string; help: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-border px-4 py-3 last:border-b-0">
      <div className="min-w-0 flex-1 basis-56">
        <p className="dash-control font-medium text-fg">{title}</p>
        <p className="mt-0.5 dash-support text-fg-muted">{help}</p>
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  )
}

/**
 * In-app preferences, one card row per setting. Help text for the stored fields comes from the
 * manifest group (consolePrefsSettingsGroup), so swapping this for kit-settings' <SettingsRenderer>
 * changes the chrome, not the data. Edits apply immediately (no draft/save step).
 */
export function ConsolePrefsPanel({ prefs, onChange }: ConsolePrefsPanelProps) {
  const props = consolePrefsSettingsGroup().schema.properties
  const help = (field: string) => String(props[field]?.description ?? '')
  const [adding, setAdding] = useState(false)
  const [draft, setDraft] = useState('')
  const addSource = () => {
    if (draft.trim()) onChange({ mutedSources: [...prefs.mutedSources, draft] })
    setDraft('')
    setAdding(false)
  }
  const window = WINDOWS.find(([s]) => s === prefs.coalesceWindowSec)?.[1] ?? `${prefs.coalesceWindowSec} seconds`

  return (
    <form aria-label="Alert preferences" className="space-y-4" onSubmit={(e) => e.preventDefault()}>
      <div className="rounded-md border border-border">
        <Row title="Notify me about" help="Sets what appears in the bell, the list and toasts.">
          <Segmented
            label="Notify me about"
            value={levelOf(prefs)}
            onChange={(id) => onChange(levelPatch(id))}
            bare
            items={NOTIFY_LEVELS.map((l) => ({ value: l.id, label: l.label }))}
          />
        </Row>
        <p className="sr-only" aria-live="polite">{previewLine(levelOf(prefs), prefs.quiet)}</p>
        <Row title="Pop-up toasts" help="Critical alerts always pop up and stay until dismissed.">
          <Switch aria-label="Pop-up toasts" checked={prefs.toastsEnabled} onCheckedChange={(v) => onChange({ toastsEnabled: v })} />
        </Row>
      </div>

      <div className="rounded-md border border-border">
        <Row title="Quiet" help={help('quiet')}>
          <Switch aria-label="Quiet mode" checked={prefs.quiet} onCheckedChange={(v) => onChange({ quiet: v })} />
        </Row>
        <Row title="Quiet hours" help="Repeats every day in your local time.">
          <Switch aria-label="Quiet hours" checked={prefs.quietHoursEnabled} onCheckedChange={(v) => onChange({ quietHoursEnabled: v })} />
          <input type="time" aria-label="Quiet hours start" value={prefs.quietHoursStart} disabled={!prefs.quietHoursEnabled} onChange={(e) => e.target.value && onChange({ quietHoursStart: e.target.value })} className={INPUT} />
          <span className="dash-control text-fg-muted">to</span>
          <input type="time" aria-label="Quiet hours end" value={prefs.quietHoursEnd} disabled={!prefs.quietHoursEnabled} onChange={(e) => e.target.value && onChange({ quietHoursEnd: e.target.value })} className={INPUT} />
        </Row>
      </div>

      <div className="rounded-md border border-border">
        <Row title="Muted sources" help="Agents or machines that never raise a toast. Critical still does.">
          <ul aria-label="Muted sources" className="flex flex-wrap items-center justify-end gap-1.5">
            {prefs.mutedSources.map((s) => (
              <li key={s}>
                <Chip className="gap-1.5 py-0.5">
                  {s}
                  <button type="button" aria-label={`Unmute ${s}`} onClick={() => onChange({ mutedSources: prefs.mutedSources.filter((m) => m !== s) })} className="text-fg-muted hover:text-fg">
                    <X aria-hidden className="size-3" />
                  </button>
                </Chip>
              </li>
            ))}
            <li>
              {adding ? (
                <input
                  autoFocus
                  aria-label="Source to mute"
                  placeholder="agent-bravo"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onBlur={addSource}
                  onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addSource() } if (e.key === 'Escape') { setDraft(''); setAdding(false) } }}
                  className={`${INPUT} w-36`}
                />
              ) : (
                <Button type="button" variant="outline" size="xs" onClick={() => setAdding(true)}>+ Add source</Button>
              )}
            </li>
          </ul>
        </Row>
        <Row title="Group repeats" help="Same alert within this window becomes one entry (×4).">
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button type="button" variant="outline" size="sm" aria-label="Group repeats window" />}>{window} ▾</DropdownMenuTrigger>
            <DropdownMenuContent aria-label="Group repeats window" align="end" data-dashboard-shape="square" className="w-40">
              {WINDOWS.map(([s, l]) => <DropdownMenuItem key={s} onClick={() => onChange({ coalesceWindowSec: s })}>{l}</DropdownMenuItem>)}
            </DropdownMenuContent>
          </DropdownMenu>
        </Row>
      </div>

      <div className="rounded-md border border-dashed border-border px-4 py-3">
        <p className="font-mono dash-caption uppercase tracking-label text-fg-faint">What you will see</p>
        <p className="mt-1 dash-control text-fg-muted" aria-live="polite">{seeLine(prefs)}</p>
      </div>
    </form>
  )
}
