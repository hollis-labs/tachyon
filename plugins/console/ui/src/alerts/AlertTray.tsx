import { useEffect, useRef, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { BellOff, X } from 'lucide-react'
import { SEVERITY_ORDER } from '../events/types'
import { useAlertsController } from './context'
import { ConsolePrefsPanel } from '../prefs/ConsolePrefsPanel'
import { useRestoreFocus } from '../shell/restore-focus'
import { AlertListItem } from './AlertListItem'
import { SEVERITY_STYLE } from './severity-style'
import type { AlertSeverity } from './types'

const HOUR = 3_600_000
const BTN = 'font-mono uppercase tracking-label'

/** Persistent tray: active alerts and history, quiet mode and bell/list threshold. Escape or outside click closes. */
export function AlertTray() {
  const c = useAlertsController()
  const [tab, setTab] = useState<'active' | 'history'>('active')
  const [prefsOpen, setPrefsOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useRestoreFocus(c.open, '[data-alert-trigger]')

  useEffect(() => {
    if (!c.open) return
    const el = ref.current
    el?.focus()
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      // Clicks in the trigger or in a modal stacked above the tray (alert detail) are not "outside".
      if (el && !el.contains(t) && !(t as Element).closest?.('[data-alert-trigger], [data-overlay]')) c.setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [c.open])

  if (!c.open) return null
  const rows = tab === 'active' ? c.active : c.history
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label="Alerts"
      data-dashboard-shape="square"
      data-dashboard-accent="yellow"
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key === 'Escape') c.setOpen(false)
      }}
      className="fixed bottom-3 left-14 z-50 flex max-h-[min(36rem,calc(100vh-1.5rem))] w-[min(26rem,calc(100vw-4.5rem))] flex-col border border-border bg-bg-elevated shadow-xl shadow-black/30 focus-visible:outline-none"
    >
      <header className="flex items-center gap-2 border-b border-border-subtle px-4 py-2.5">
        <h2 className="flex-1 font-mono text-label uppercase tracking-label text-fg-secondary">
          Alerts{c.unread > 0 ? ` · ${c.unread} unread` : ''}
        </h2>
        <Button type="button" variant="outline" size="xs" className={BTN} onClick={c.markAllRead} disabled={c.unread === 0}>
          Mark all read
        </Button>
        <Button type="button" variant="ghost" size="icon-xs" onClick={() => c.setOpen(false)} aria-label="Close alerts" className="text-fg-muted">
          <X aria-hidden="true" />
        </Button>
      </header>

      <div className="flex items-center gap-3 border-b border-border-subtle px-4 py-2 font-mono text-label uppercase tracking-label">
        <label className="flex items-center gap-1.5 text-fg-secondary">
          <input
            type="checkbox"
            checked={c.prefs.quiet}
            onChange={(e) => c.setPrefs({ quiet: e.target.checked })}
            className="h-3 w-3 accent-primary"
          />
          <BellOff aria-hidden="true" className="h-3 w-3" />
          Quiet
        </label>
        <label className="ml-auto flex items-center gap-1.5 text-fg-secondary">
          Bell / list
          <select
            aria-label="Bell / list threshold"
            value={c.prefs.bellMinSeverity}
            onChange={(e) => c.setPrefs({ bellMinSeverity: e.target.value as AlertSeverity })}
            className="border border-border bg-bg-elevated px-1 py-0.5 text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
          >
            {SEVERITY_ORDER.map((s) => (
              <option key={s} value={s}>
                {s === 'debug' ? 'all' : `${SEVERITY_STYLE[s].label.toLowerCase()}+`}
              </option>
            ))}
          </select>
        </label>
        <Button type="button" variant="outline" size="xs" aria-expanded={prefsOpen} onClick={() => setPrefsOpen((v) => !v)} className={BTN}>
          Prefs
        </Button>
      </div>
      {prefsOpen && <ConsolePrefsPanel prefs={c.prefs} onChange={c.setPrefs} />}

      <div role="tablist" aria-label="Alert views" className="flex border-b border-border-subtle">
        {(['active', 'history'] as const).map((t) => {
          const n = t === 'active' ? c.active.length : c.history.length
          return (
            <button
              key={t}
              role="tab"
              type="button"
              aria-selected={tab === t}
              onClick={() => setTab(t)}
              className={`flex-1 border-b-2 px-4 py-2 font-mono text-label uppercase tracking-label focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring ${tab === t ? 'border-primary text-fg' : 'border-transparent text-fg-muted hover:text-fg-secondary'}`}
            >
              {t} · {n}
            </button>
          )
        })}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {rows.length === 0 ? (
          <p className="px-4 py-8 text-center font-mono text-xs text-fg-muted">{c.prefs.bellMinSeverity !== 'debug' ? 'No alerts at this threshold.' : tab === 'active' ? 'Nothing needs you right now.' : 'No dismissed or snoozed alerts.'}</p>
        ) : (
          <ul aria-label={tab === 'active' ? 'Active alerts' : 'Alert history'}>
            {rows.map(({ alert, status, snoozeUntil }) => (
              <AlertListItem
                key={alert.id}
                alert={alert}
                status={status}
                snoozeUntil={snoozeUntil}
                now={c.now}
                onRead={() => c.markRead(alert.id)}
                onDismiss={tab === 'active' ? () => c.dismiss(alert.id) : undefined}
                onSnooze={tab === 'active' ? () => c.snooze(alert.id, HOUR) : undefined}
                onRestore={tab === 'history' ? () => c.restore(alert.id) : undefined}
                onExpand={() => c.expand(alert.id)}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
