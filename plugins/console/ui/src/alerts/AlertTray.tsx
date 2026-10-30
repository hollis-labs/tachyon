import { useEffect, useRef, useState } from 'react'
import { useConsoleState } from '../events/hooks'
import { Button } from '@hollis-labs/design-components'
import { BellOff, Settings, X } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useAlertsController } from './context'
import { useRestoreFocus } from '../shell/restore-focus'
import { Segmented } from '../components/segmented'
import { AlertListItem } from './AlertListItem'
import { groupActive, groupHistory } from './group'

const HOUR = 3_600_000
const BTN = ''

/** Persistent tray: active alerts and history, quiet mode and bell/list threshold. Escape or outside click closes. */
export function AlertTray() {
  const c = useAlertsController()
  const [tab, setTab] = useState<'active' | 'history'>('active')
  const ref = useRef<HTMLDivElement>(null)
  const consoleState = useConsoleState()
  const feed = c.feed ?? consoleState
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
            tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key === 'Escape') { c.setOpen(false); return }
        const t = e.target as HTMLElement
        if (e.metaKey || e.ctrlKey || e.altKey || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable) return
        // Up / Down move between rows; S snoozes and D dismisses the focused row; Shift+R marks all read.
        const opens = [...(ref.current?.querySelectorAll<HTMLElement>('button[aria-label^="Open details"]') ?? [])]
        const here = opens.indexOf(t)
        const row = t.closest<HTMLElement>('[data-alert-id]')
        const id = row?.dataset.alertId
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          if (!opens.length) return
          e.preventDefault()
          const next = e.key === 'ArrowDown' ? Math.min(here + 1, opens.length - 1) : Math.max(here <= 0 ? 0 : here - 1, 0)
          opens[here === -1 ? 0 : next]?.focus()
        } else if (e.key === 's' && id && tab === 'active') {
          e.preventDefault()
          const after = opens[here + 1] ?? opens[here - 1]
          c.snooze(id, HOUR)
          after?.focus()
        } else if (e.key === 'd' && id && tab === 'active') {
          e.preventDefault()
          const after = opens[here + 1] ?? opens[here - 1]
          c.dismiss(id)
          after?.focus()
        } else if (e.key === 'R' && e.shiftKey) {
          e.preventDefault()
          c.markAllRead()
        }
      }}
      className="fixed bottom-3 left-14 z-50 flex max-h-[min(36rem,calc(100vh-1.5rem))] w-[min(26rem,calc(100vw-4.5rem))] max-md:!bottom-0 max-md:!left-0 max-md:max-h-[82dvh] max-md:!w-full max-md:rounded-t-xl flex-col border border-border bg-bg-elevated shadow-xl shadow-black/30 focus-visible:outline-none"
    >
      <header className="flex items-center gap-2 border-b border-border-subtle px-4 py-2.5">
        <h2 className="flex-1 font-sans text-sm font-semibold normal-case tracking-normal text-fg">
          Alerts{c.unread > 0 ? <span className="ml-2 font-normal text-fg-muted">{c.unread} unread</span> : ''}
        </h2>
        <Button type="button" variant="ghost" size="xs" className={BTN} onClick={c.markAllRead} disabled={c.unread === 0}>
          Mark all read
        </Button>
        <Button type="button" variant="ghost" size="icon-xs" aria-pressed={c.prefs.quiet} aria-label="Quiet" title="Quiet" onClick={() => c.setPrefs({ quiet: !c.prefs.quiet })} className="text-fg-muted">
          <BellOff aria-hidden="true" />
        </Button>
        <Link to="/settings/alerts" onClick={() => c.setOpen(false)} aria-label="Alert settings" title="Alert settings" className="inline-flex h-6 w-6 items-center justify-center text-fg-muted hover:text-fg">
          <Settings aria-hidden="true" className="h-3.5 w-3.5" />
        </Link>
        <Button type="button" variant="ghost" size="icon-xs" onClick={() => c.setOpen(false)} aria-label="Close alerts" className="text-fg-muted">
          <X aria-hidden="true" />
        </Button>
      </header>

      {feed.ready && feed.status !== 'live' && (
        <p role="status" className="border-b border-border-subtle bg-surface/40 px-4 py-1.5 text-xs text-fg-muted">
          {feed.status === 'error' ? 'Alerts are not updating.' : 'Reconnecting…'} What you see may be out of date.
        </p>
      )}
      {c.error && <p role="status" className="border-b border-border-subtle bg-surface/40 px-4 py-1.5 text-xs text-fg-muted">{c.error}</p>}
      {c.prefs.quiet && (
        <div role="status" className="flex items-center gap-2 border-b border-border-subtle bg-surface/40 px-4 py-1.5 text-xs text-fg-muted">
          <BellOff aria-hidden="true" className="h-3 w-3" />
          <span>Quiet is on. Only critical alerts pop up.</span>
          <button type="button" className="ml-auto underline underline-offset-2 hover:text-fg" onClick={() => c.setPrefs({ quiet: false })}>Turn off</button>
        </div>
      )}

      <Segmented label="Alert views" value={tab} onChange={setTab} className="border-border-subtle" items={[
        { value: 'active' as const, label: 'Active', count: c.active.length },
        { value: 'history' as const, label: 'History', count: c.history.length },
      ]} />

      <div className="min-h-0 flex-1 overflow-y-auto">
        {rows.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-fg-muted">{c.prefs.bellMinSeverity !== 'debug' ? 'No alerts at this level.' : tab === 'active' ? 'Nothing needs you right now.' : 'No dismissed or snoozed alerts.'}</p>
        ) : (
          <ul aria-label={tab === 'active' ? 'Active alerts' : 'Alert history'}>
            {(tab === 'active' ? groupActive(rows) : groupHistory(rows, c.now)).map((g) => (
              <li key={g.id} aria-label={g.label} className="list-none">
                <h3 className="border-b border-border-subtle bg-surface/40 px-4 py-1 font-sans text-xs font-medium normal-case tracking-normal text-fg-muted">{g.label} <span className="text-fg-faint">{g.items.length}</span></h3>
                <ul>
                  {g.items.map(({ alert, status, snoozeUntil }) => (
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
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
