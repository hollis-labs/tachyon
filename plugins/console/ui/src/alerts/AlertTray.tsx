import { useEffect, useRef, useState } from 'react'
import { useConsoleState } from '../events/hooks'
import { Button, OverflowMenu } from '@hollis-labs/design-components'
import { BellOff, X } from 'lucide-react'
import { Link, useNavigate } from 'react-router-dom'
import { useAlertsController } from './context'
import { useRestoreFocus } from '../shell/restore-focus'
import { Chip } from '../components/chip'
import { PillTabs } from '../components/pill-tabs'
import { levelOf } from '../prefs/levels'
import { AlertListItem } from './AlertListItem'
import { groupActive, groupHistory, needsYou } from './group'

const HOUR = 3_600_000
const LEVEL_LINE = { blockers: 'Showing blockers only', warnings: 'Showing warnings and up', everything: 'Showing everything' } as const

/** Persistent tray: active alerts and history, quiet mode and bell/list threshold. Escape or outside click closes. */
export function AlertTray() {
  const c = useAlertsController()
  const navigate = useNavigate()
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
  const needs = c.active.filter((e) => needsYou(e.alert)).length
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
      className="fixed bottom-3 left-14 z-50 flex max-h-[min(36rem,calc(100vh-1.5rem))] w-[min(26rem,calc(100vw-4.5rem))] max-md:!bottom-0 max-md:!left-0 max-md:max-h-[82dvh] max-md:!w-full max-md:rounded-t-xl flex-col border border-border bg-bg-elevated shadow-[0_12px_32px_rgb(0_0_0/calc(.3*var(--sh)))] focus-visible:outline-none"
    >
      <header className="flex items-center gap-3 border-b border-border-subtle px-4 py-2.5">
        <h2 className="font-mono dash-caption uppercase tracking-label text-fg">Alerts</h2>
        {needs > 0 && <Chip tone="danger">{needs} need you</Chip>}
        <PillTabs
          size="sm"
          label="Alert views"
          value={tab}
          onChange={(v) => setTab(v as 'active' | 'history')}
          items={[{ value: 'active', label: 'Active' }, { value: 'history', label: 'History' }]}
          className="ml-auto w-auto"
        />
        <OverflowMenu
          ariaLabel="Alert options"
          actions={[
            { label: 'Mark all read', onSelect: c.markAllRead, disabled: c.unread === 0 },
            { label: `${c.prefs.quiet ? '✓ ' : ''}Quiet`, onSelect: () => c.setPrefs({ quiet: !c.prefs.quiet }) },
            { label: 'Alert settings', onSelect: () => { c.setOpen(false); navigate('/settings/alerts') } },
          ]}
        />
        <Button type="button" variant="ghost" size="icon-xs" onClick={() => c.setOpen(false)} aria-label="Close alerts" className="text-fg-muted md:hidden">
          <X aria-hidden="true" />
        </Button>
      </header>

      {feed.ready && feed.status !== 'live' && (
        <p role="status" className="flex items-center gap-2 border-b border-border-subtle bg-surface/40 px-4 py-1.5 dash-support text-fg-muted">
          <span>{feed.status === 'error' ? 'Alerts are not updating.' : 'Reconnecting…'} What you see may be out of date.</span>
          {c.feed?.retry && <Button type="button" variant="outline" size="xs" className="ml-auto" onClick={c.feed.retry}>Retry</Button>}
        </p>
      )}
      {!!c.feed?.sourcesDown?.length && (
        <p role="status" className="border-b border-border-subtle bg-surface/40 px-4 py-1.5 dash-support text-fg-muted">
          Alerts from {c.feed.sourcesDown.map((s) => s.charAt(0).toUpperCase() + s.slice(1)).join(', ')} are not updating. Other alerts are current.
        </p>
      )}
      {c.error && <p role="status" className="border-b border-border-subtle bg-surface/40 px-4 py-1.5 dash-support text-fg-muted">{c.error}</p>}
      {c.prefs.quiet && (
        <div role="status" className="flex items-center gap-2 border-b border-border-subtle bg-surface/40 px-4 py-1.5 dash-support text-fg-muted">
          <BellOff aria-hidden="true" className="h-3 w-3" />
          <span>{c.prefs.quietHoursEnabled ? `Quiet until ${c.prefs.quietHoursEnd}` : 'Quiet until you turn it off'} · critical still gets through</span>
          <button type="button" className="ml-auto underline underline-offset-2 hover:text-fg" onClick={() => c.setPrefs({ quiet: false })}>Turn off</button>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {rows.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-fg-muted">{c.prefs.bellMinSeverity !== 'debug' ? 'No alerts at this level.' : tab === 'active' ? 'Nothing needs you right now' : 'No dismissed or snoozed alerts.'}</p>
        ) : (
          <ul aria-label={tab === 'active' ? 'Active alerts' : 'Alert history'}>
            {(tab === 'active' ? groupActive(rows, c.now) : groupHistory(rows, c.now)).map((g) => (
              <li key={g.id} aria-label={g.label} className="list-none">
                {g.id === 'needs'
                  ? <h3 className="flex items-center justify-between px-4 pb-1 pt-3 font-mono dash-caption uppercase tracking-label text-fg-faint">{g.label} <span>{g.items.length}</span></h3>
                  : <h3 className="px-4 pb-1 pt-3 normal-case tracking-normal"><span className="rounded-full border border-border px-2 py-0.5 font-sans dash-caption text-fg-muted">{g.label}</span></h3>}
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
      <footer className="flex items-center justify-between gap-2 border-t border-border-subtle px-4 py-2 dash-support text-fg-muted">
        <span>{LEVEL_LINE[levelOf(c.prefs)]}</span>
        <Link to="/settings/alerts" onClick={() => c.setOpen(false)} className="hover:text-fg">Alert settings ↗</Link>
      </footer>
    </div>
  )
}
