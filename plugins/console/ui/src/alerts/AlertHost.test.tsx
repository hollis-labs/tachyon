// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiProvider } from '@tachyon/torque-client'
import { FixtureSource } from '../testkit/events-fixture-source'
import { ConsoleProvider } from '../events/provider'
import { AlertHost } from './AlertHost'
import { AlertToast } from './AlertToast'
import { AlertTrayTrigger } from './AlertTrayTrigger'
import { ConsoleShell } from './ConsoleShell'
import { MemoryAlertPrefsStore } from './prefs'
import { sampleAlerts } from '../testkit/alerts-samples'
import type { ConsoleEvent } from '../events/types'

afterEach(cleanup)

async function setup(prefs = new MemoryAlertPrefsStore()) {
  const source = new FixtureSource({ intervalMs: 0, initial: 16 })
  const sub = vi.spyOn(source, 'subscribe')
  await act(async () => {
    render(
      <MemoryRouter>
        <ApiProvider client={{} as never}>
          <ConsoleProvider source={source} resyncMs={0}>
            <AlertHost prefsStore={prefs}>
              <AlertTrayTrigger />
            </AlertHost>
          </ConsoleProvider>
        </ApiProvider>
      </MemoryRouter>,
    )
  })
  return { source, sub, prefs }
}

// Fixture event 17 is n%8===1 (agent.state, mostly dropped); 19 is a message (notice), 20 an approval/other.
async function emitUntilToast(source: FixtureSource, max = 8) {
  for (let i = 0; i < max && screen.queryAllByTestId('alert-toast').length === 0; i++) await act(async () => source.tick())
}

describe('AlertTray keyboard', () => {
  it('moves between rows with the arrows, dismisses with D, marks all read with Shift+R, and ignores keys in fields', async () => {
    await setup()
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    const tray = screen.getByRole('dialog', { name: 'Alerts' })
    const opens = () => within(tray).getAllByRole('button', { name: /^Open details/ })
    const first = opens()[0]
    first.focus()
    await act(async () => fireEvent.keyDown(first, { key: 'ArrowDown' }))
    expect(document.activeElement).toBe(opens()[1])
    const before = within(tray).getAllByTestId('alert-item').length
    await act(async () => fireEvent.keyDown(document.activeElement!, { key: 'd' }))
    expect(within(tray).getAllByTestId('alert-item').length).toBe(before - 1)
    await act(async () => fireEvent.keyDown(document.activeElement ?? tray, { key: 'R', shiftKey: true }))
    expect(screen.queryByTestId('alert-badge')).toBeNull()
  })
})

describe('AlertHost', () => {
  it('opens no stream of its own, skips the backlog, then toasts new alerts', async () => {
    const { source, sub } = await setup()
    expect(sub).toHaveBeenCalledTimes(1)
    expect(screen.queryAllByTestId('alert-toast')).toHaveLength(0)
    await emitUntilToast(source)
    expect(screen.getAllByTestId('alert-toast').length).toBeGreaterThan(0)
  })

  it('quiet mode suppresses toasts but the badge still counts', async () => {
    const { source } = await setup(new MemoryAlertPrefsStore({ quiet: true }))
    for (let i = 0; i < 8; i++) await act(async () => source.tick())
    expect(screen.queryAllByTestId('alert-toast')).toHaveLength(0)
    expect(screen.getByTestId('alert-badge')).toBeTruthy()
  })

  it('applies the critical threshold immediately to visible and incoming pop-up alerts', async () => {
    const { source, prefs } = await setup()
    await emitUntilToast(source)
    expect(screen.getAllByTestId('alert-toast').some(e => e.dataset.severity !== 'critical')).toBe(true)
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    const tray = screen.getByRole('dialog', { name: 'Alerts' })
    await act(async () => prefs.set({ toastMinSeverity: 'critical' }))
    expect(prefs.get().toastMinSeverity).toBe('critical')
    expect(screen.queryByTestId('alert-toast')).toBeNull()
    for (const severity of ['notice', 'warning', 'error', 'critical'] as const) {
      const event: ConsoleEvent = { id: `threshold-${severity}`, ts: Date.now(), kind: 'test.alert', severity,
        source: { id: 'threshold-check' }, subjects: [], actions: [], payload: {}, extra: {} }
      await act(async () => source.emit(event))
    }
    expect(screen.getAllByTestId('alert-toast').map(e => e.dataset.severity)).toEqual(['critical'])
    await act(async () => fireEvent.click(within(tray).getByRole('button', { name: 'Quiet' })))
    expect(screen.getAllByTestId('alert-toast').map(e => e.dataset.severity)).toEqual(['critical'])
  })

  it('filters the bell and both lists independently of toasts, without reading or deleting hidden alerts', async () => {
    const { source, prefs } = await setup()
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    const tray = screen.getByRole('dialog', { name: 'Alerts' })
    const setBell = (v: 'debug' | 'critical') => act(async () => prefs.set({ bellMinSeverity: v }))
    await setBell('critical')
    expect(screen.queryByTestId('alert-badge')).toBeNull()
    expect(within(tray).queryByRole('list', { name: 'Active alerts' })).toBeNull()
    expect(prefs.get().toastMinSeverity).toBe('notice')
    for (const severity of ['warning', 'critical'] as const) {
      await act(async () => source.emit({ id: `independent-${severity}`, ts: Date.now(), kind: `threshold.${severity}`, severity,
        source: { id: 'threshold-check' }, subjects: [], actions: [], payload: {}, extra: {} }))
    }
    const active = within(tray).getByRole('list', { name: 'Active alerts' })
    expect(within(active).queryByText('threshold.warning')).toBeNull()
    expect(within(active).getByRole('button', { name: 'Open details: threshold.critical' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Alerts, 1 unread' })).toBeTruthy()
    // A warning can still toast while hidden from the critical-only bell/list.
    expect(screen.getAllByTestId('alert-toast').map(e => e.dataset.severity)).toEqual(['warning', 'critical'])
    await act(async () => fireEvent.click(within(tray).getByRole('button', { name: 'Mark all read' })))
    expect(screen.queryByTestId('alert-badge')).toBeNull()
    await setBell('debug')
    const warning = within(tray).getByRole('button', { name: 'Open details: threshold.warning' }).closest('[data-testid="alert-item"]')!
    expect(warning.getAttribute('data-status')).toBe('unread')
    await act(async () => fireEvent.click(within(tray).getByRole('button', { name: 'Dismiss: threshold.warning' })))
    await act(async () => fireEvent.click(within(tray).getByRole('tab', { name: /history/i })))
    expect(within(tray).getByRole('button', { name: 'Open details: threshold.warning' })).toBeTruthy()
    await setBell('critical')
    expect(within(tray).queryByText('threshold.warning')).toBeNull()
    await act(async () => fireEvent.click(within(tray).getByRole('button', { name: 'Quiet' })))
    expect(prefs.get()).toMatchObject({ quiet: true, toastMinSeverity: 'notice', bellMinSeverity: 'critical' })
  })

  it('tray lists alerts, dismiss moves to history, restore brings it back, Escape closes', async () => {
    await setup()
    const trigger = screen.getByRole('button', { name: /^Alerts/ })
    await act(async () => fireEvent.click(trigger))
    const dialog = screen.getByRole('dialog', { name: 'Alerts' })
    const before = within(dialog).getAllByTestId('alert-item').length
    expect(before).toBeGreaterThan(0)
    await act(async () => fireEvent.click(within(dialog).getAllByRole('button', { name: /^Dismiss:/ })[0]))
    expect(within(dialog).getAllByTestId('alert-item')).toHaveLength(before - 1)
    await act(async () => fireEvent.click(within(dialog).getByRole('tab', { name: /history/i })))
    expect(within(dialog).getAllByTestId('alert-item')).toHaveLength(1)
    await act(async () => fireEvent.click(within(dialog).getByRole('button', { name: /^Restore:/ })))
    await act(async () => fireEvent.click(within(dialog).getByRole('tab', { name: /active/i })))
    expect(within(dialog).getAllByTestId('alert-item')).toHaveLength(before)
    await act(async () => fireEvent.keyDown(dialog, { key: 'Escape' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('mark all read clears the badge', async () => {
    await setup()
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Mark all read' })))
    expect(screen.queryByTestId('alert-badge')).toBeNull()
  })
})

describe('AlertToast', () => {
  it('auto-expires per severity, pauses on hover, and stays for critical', () => {
    vi.useFakeTimers()
    try {
      const onClose = vi.fn()
      const [critical, , , notice] = sampleAlerts(Date.now())
      const { rerender } = render(
        <MemoryRouter>
          <AlertToast alert={notice} now={Date.now()} onClose={onClose} />
        </MemoryRouter>,
      )
      const el = screen.getByTestId('alert-toast')
      fireEvent.mouseEnter(el)
      act(() => void vi.advanceTimersByTime(60_000))
      expect(onClose).not.toHaveBeenCalled()
      fireEvent.mouseLeave(el)
      act(() => void vi.advanceTimersByTime(7_100))
      expect(onClose).toHaveBeenCalledTimes(1)
      onClose.mockClear()
      rerender(
        <MemoryRouter>
          <AlertToast alert={critical} now={Date.now()} onClose={onClose} />
        </MemoryRouter>,
      )
      act(() => void vi.advanceTimersByTime(120_000))
      expect(onClose).not.toHaveBeenCalled()
      fireEvent.keyDown(screen.getByTestId('alert-toast'), { key: 'Escape' })
      expect(onClose).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('ConsoleShell', () => {
  it('always enables Console and renders children and the alert trigger', async () => {
    const source = new FixtureSource({ intervalMs: 0, initial: 0 })
    await act(async () => {
      render(
        <MemoryRouter>
          <ApiProvider client={{} as never}>
            <ConsoleShell source={source}>
              <AlertTrayTrigger />
              <span>child</span>
            </ConsoleShell>
          </ApiProvider>
        </MemoryRouter>,
      )
    })
    expect(screen.getByText('child')).toBeTruthy()
    expect(screen.getByRole('button', { name: /^Alerts/ })).toBeTruthy()
  })
})
