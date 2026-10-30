// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ApiProvider } from '@tachyon/torque-client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConsoleProvider } from '../../events/provider'
import { FixtureSource } from '../../testkit/events-fixture-source'
import { DEFAULT_CONSOLE_PREFS } from '../../prefs/types'
import { AlertTrayTrigger } from '../AlertTrayTrigger'
import { ApprovalProvider } from '../../approvals/context'
import type { ApprovalClient, ApprovalItem } from '../../approvals/client'
import { AlertsApiError, type AlertsClient } from './client'
import { fromServerPrefs, toServerPrefs } from './prefs-store'
import { ServerAlertHost } from './ServerAlertHost'
import { ServerAlertsStore } from './store'
import type { ServerAlert, ServerAlertDetail, ServerCounts, ServerFrame, ServerPrefs } from './types'

afterEach(cleanup)

const alert = (id: string, over: Partial<ServerAlert> = {}): ServerAlert => ({
  alert_id: id, seq: 1, kind: 'task.failed', rule_id: 'r', severity: 'warning', severity_base: 'warning', needs_you: false,
  title: `Alert ${id}`, summary: '', source: 'Builder', icon: 'warning', status: 'active', read: false, count: 1, coalesce_key: '',
  first_at_ms: 1000, last_at_ms: 1000, snooze_until_ms: null, wake_at_ms: null, note: '', resolution: '', resolved_by: '', primary: null,
  subjects: [], escalate_at_ms: null, escalated_at_ms: null, version: 1, created_ms: 1000, updated_ms: 1000, ...over,
})
const counts = (n = 0): ServerCounts => ({ unread: n, active: n, needs_you: 0, by_severity: {}, seq: 1 })
const prefs: ServerPrefs = { notify_level: 'warnings', toasts: true, quiet: false, quiet_hours: { enabled: false, start: '22:00', end: '07:00', tz: 'Europe/Berlin' }, muted_sources: [], coalesce_window_sec: 120 }

let detailOf: (id: string) => ServerAlertDetail = (id) => ({
  alert: alert(id), timeline: [{ at_ms: 1000, label: 'This alert' }], linked: [], technical: { fields: [{ label: 'task', value: 'CW-1' }] }, actions: [], approval: null, server_now_ms: 2000,
})

function fakeClient(active: ServerAlert[], history: ServerAlert[] = []) {
  const c = {
    list: vi.fn(async (view: 'active' | 'history') => ({ alerts: view === 'active' ? active : history, next: null, counts: counts(active.length), seq: 5, server_now_ms: Date.now() })),
    counts: vi.fn(),
    detail: vi.fn(async (id: string) => detailOf(id)),
    op: vi.fn(async (id: string, op: { op: string }, v: number) => ({ ...alert(id), version: v + 1, read: op.op === 'read' })),
    bulk: vi.fn(async () => ({ updated: [], skipped: [], counts: counts(0), seq: 6 })),
    prefs: vi.fn(async () => prefs),
    savePrefs: vi.fn(async (p: ServerPrefs) => p),
    migratePrefs: vi.fn(),
    sources: vi.fn(async () => ({ sources: [{ source: 'torque', state: 'down', last_event_ms: 0, note: 'HTTP 401' }, { source: 'activity', state: 'ok', last_event_ms: 1, note: '' }], server_now_ms: 1 })),
    streamUrl: vi.fn((after?: number) => `/stream?after=${after ?? ''}`),
  }
  return c as typeof c & AlertsClient
}
const frame = (type: ServerFrame['type'], a?: ServerAlert, over: Partial<ServerFrame> = {}): ServerFrame => ({ type, at_ms: 1, alert: a, ...over })

describe('ServerAlertsStore', () => {
  it('loads active and history, then applies stream frames and only toasts when the server says so', async () => {
    const client = fakeClient([alert('al_1')], [alert('al_old', { status: 'dismissed' })])
    const store = new ServerAlertsStore(client, () => ({}) as EventSource)
    const toasted: string[] = []
    store.onToast((a) => toasted.push(a.alert_id))
    await store.load()
    expect(store.getSnapshot().alerts.map((a) => a.alert_id).sort()).toEqual(['al_1', 'al_old'])
    store.apply(frame('alert.created', alert('al_2', { last_at_ms: 2000 }), { toast: true }))
    store.apply(frame('alert.updated', alert('al_1', { version: 2, count: 3, last_at_ms: 3000 }), { toast: false, reason: 'coalesced' }))
    expect(toasted).toEqual(['al_2'])
    expect(store.getSnapshot().alerts[0].alert_id).toBe('al_1')
    store.apply(frame('alert.updated', alert('al_1', { version: 1, count: 1 })))
    expect(store.getSnapshot().alerts.find((a) => a.alert_id === 'al_1')?.count).toBe(3)
    store.apply(frame('alert.removed', alert('al_2')))
    expect(store.getSnapshot().alerts.some((a) => a.alert_id === 'al_2')).toBe(false)
  })
  it('refetches on resync', async () => {
    const client = fakeClient([alert('al_1')])
    const store = new ServerAlertsStore(client, () => ({}) as EventSource)
    await store.load()
    store.apply({ type: 'resync', at_ms: 1 })
    await vi.waitFor(() => expect(client.list).toHaveBeenCalledTimes(4))
  })
  it('sends expected_version, shows the change at once, and adopts the server copy on a conflict', async () => {
    const client = fakeClient([alert('al_1', { version: 4 })])
    const store = new ServerAlertsStore(client, () => ({}) as EventSource)
    await store.load()
    const ok = store.op('al_1', { op: 'read' }, (a) => ({ ...a, read: true }))
    expect(store.getSnapshot().alerts[0].read).toBe(true)
    await ok
    expect(client.op).toHaveBeenCalledWith('al_1', { op: 'read' }, 4)
    client.op.mockRejectedValueOnce(new AlertsApiError(409, 'version_conflict', 'stale', alert('al_1', { version: 9, status: 'dismissed' })))
    expect(await store.op('al_1', { op: 'dismiss' }, (a) => ({ ...a, status: 'dismissed' }))).toBe(false)
    expect(store.getSnapshot().alerts[0].version).toBe(9)
    expect(store.getSnapshot().error).toMatch(/another device/)
  })
  it('puts the alert back when an op fails', async () => {
    const client = fakeClient([alert('al_1')])
    const store = new ServerAlertsStore(client, () => ({}) as EventSource)
    await store.load()
    client.op.mockRejectedValueOnce(new AlertsApiError(0, 'unavailable', 'down'))
    await store.op('al_1', { op: 'dismiss' }, (a) => ({ ...a, status: 'dismissed' }))
    expect(store.getSnapshot().alerts[0].status).toBe('active')
    expect(store.getSnapshot().error).toMatch(/did not save/)
  })
})

describe('retry', () => {
  it('refetches and opens a fresh stream after the connection gave up', async () => {
    const client = fakeClient([alert('al_1')])
    vi.stubGlobal('EventSource', class {})
    const made: string[] = []
    const store = new ServerAlertsStore(client, (u) => { made.push(u); return {} as EventSource })
    await store.load()
    await store.retry()
    expect(client.list).toHaveBeenCalledTimes(4)
    expect(made).toEqual(['/stream?after=5'])
    vi.unstubAllGlobals()
  })
})

describe('server prefs mapping', () => {
  it('derives the bell and toast thresholds from notify_level and keeps the zone', () => {
    const p = fromServerPrefs({ ...prefs, notify_level: 'everything', quiet: true })
    expect(p).toMatchObject({ bellMinSeverity: 'debug', toastMinSeverity: 'notice', quiet: true })
    const back = toServerPrefs({ ...DEFAULT_CONSOLE_PREFS, bellMinSeverity: 'error', toastMinSeverity: 'error' }, prefs)
    expect(back).toMatchObject({ notify_level: 'blockers', toasts: true })
    expect(back.quiet_hours.tz).toBe('Europe/Berlin')
  })
})

describe('ServerAlertHost', () => {
  it('shows server alerts in the tray and marks one read with its version', async () => {
    const client = fakeClient([alert('al_1', { version: 7, title: 'Deploy failed', severity: 'error', needs_you: true })])
    await act(async () => {
      render(
        <MemoryRouter>
          <ApiProvider client={{} as never}>
            <ConsoleProvider source={new FixtureSource({ intervalMs: 0, initial: 0 })} resyncMs={0}>
              <ServerAlertHost client={client}>
                <AlertTrayTrigger />
              </ServerAlertHost>
            </ConsoleProvider>
          </ApiProvider>
        </MemoryRouter>,
      )
    })
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    const tray = screen.getByRole('dialog', { name: 'Alerts' })
    expect(within(tray).getByText('Deploy failed')).toBeTruthy()
    expect(within(tray).getByText(/Alerts from Torque are not updating/)).toBeTruthy()
    expect(within(tray).getByText('Needs you')).toBeTruthy()
    await act(async () => fireEvent.click(within(tray).getByRole('button', { name: /Mark all read/ })))
    expect(client.bulk).toHaveBeenCalledWith('read', { ids: ['al_1'] })
  })
})


describe('ServerAlertDetail', () => {
  const approvalItem: ApprovalItem = {
    item_id: 'apr_1', state: 'presented', revision: 2, enqueued_at: '2026-10-30T10:00:00Z', updated_at: '2026-10-30T10:00:00Z',
    request_snapshot: { kind: 'approval', class: 'high', title: 'Restart station', request: 'Restart it.', source: { agent_id: 'a', application_id: 'tether' } },
  }
  async function open(client: ReturnType<typeof fakeClient>, approvals: ApprovalClient) {
    await act(async () => {
      render(
        <MemoryRouter>
          <ApiProvider client={{} as never}>
            <ConsoleProvider source={new FixtureSource({ intervalMs: 0, initial: 0 })} resyncMs={0}>
              <ApprovalProvider client={approvals}>
                <ServerAlertHost client={client}>
                  <AlertTrayTrigger />
                </ServerAlertHost>
              </ApprovalProvider>
            </ConsoleProvider>
          </ApiProvider>
        </MemoryRouter>,
      )
    })
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /^Alerts/ })))
    await act(async () => fireEvent.click(within(screen.getByRole('dialog', { name: 'Alerts' })).getAllByRole('button', { name: /^Open details/ })[0]))
  }
  const approvalsClient = () => ({ audit: vi.fn().mockResolvedValue({ item_id: 'apr_1', events: [] }), inbox: vi.fn().mockResolvedValue({ pending: [approvalItem], history: [] }), present: vi.fn().mockResolvedValue(approvalItem), decide: vi.fn() }) as unknown as ApprovalClient

  it('embeds the shared Approvals decision for an approval alert and never decides through the alert', async () => {
    detailOf = (id) => ({ alert: alert(id, { icon: 'approval' }), timeline: [], linked: [], technical: { fields: [] }, actions: [], approval: { item_id: 'apr_1', expires_at_ms: 9e12, class: 'high', risk_display: 'High', provider_handoff: false }, server_now_ms: 2 })
    const client = fakeClient([alert('al_a', { icon: 'approval', kind: 'approval.requested' })])
    const approvals = approvalsClient()
    await open(client, approvals)
    const decision = await screen.findByRole('region', { name: 'Decision' })
    await within(decision).findByRole('button', { name: 'Approve' })
    expect(approvals.present).toHaveBeenCalledWith('apr_1')
    expect(client.op).not.toHaveBeenCalledWith('al_a', expect.objectContaining({ op: 'settle' }), expect.anything())
    expect(screen.queryByRole('button', { name: 'Mark handled' })).toBeNull()
  })
  it('shows a read-only card when the provider handled the approval', async () => {
    detailOf = (id) => ({ alert: alert(id), timeline: [], linked: [], technical: { fields: [] }, actions: [], approval: { item_id: 'apr_1', expires_at_ms: 9e12, class: 'low', risk_display: 'Low', provider_handoff: true }, server_now_ms: 2 })
    const client = fakeClient([alert('al_a')])
    await open(client, approvalsClient())
    await screen.findByText(/Approved through the provider/)
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
  })
  it('marks an ordinary alert handled with its version and lists technical details', async () => {
    detailOf = (id) => ({ alert: alert(id), timeline: [{ at_ms: 1000, label: 'This alert' }], linked: [], technical: { fields: [{ label: 'task', value: 'CW-1' }] }, actions: [], approval: null, server_now_ms: 2 })
    const client = fakeClient([alert('al_b', { version: 3 })])
    await open(client, approvalsClient())
    await screen.findByText('Technical details')
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Mark handled' })))
    expect(client.op).toHaveBeenCalledWith('al_b', { op: 'settle' }, 4)
  })
})

describe('derived display fields', () => {
  it('turns raw kinds and session ids into sentences', async () => {
    const { toAlert } = await import('./adapt')
    const base = { alert_id: 'a1', source: 'tether', count: 1, severity: 'warning', icon: 'bell', rule_id: 'r', needs_you: false, summary: '', coalesce_key: '', last_at_ms: 1, primary: null } as never
    const mk = (o: object) => toAlert({ ...(base as object), ...o } as never)
    expect(mk({ kind: 'message.failed', title: 'Message message.failed' }).title).toBe('Message failed')
    expect(mk({ kind: 'tool.call.finished', title: 'tool.call.finished', summary: '7709c5dd-1234-4abc-8def-0123456789ab' })).toMatchObject({ title: 'Tool call finished', detail: 'in session 7709c5dd' })
    expect(mk({ kind: 'alert.suppressed', title: '1 events dropped from activity' }).title).toBe('Activity events were dropped')
    expect(mk({ kind: 'task.state_changed', title: 'CW-1 → review' }).title).toBe('CW-1 → review')
    const pr = mk({ kind: 'pr.merged', title: 'hollis-labs/tachyon# pr.merged', severity: 'error', subjects: [{ kind: 'pr', ref: 'github:hollis-labs/tachyon#326', label: 'hollis-labs/tachyon#326' }] })
    expect(pr).toMatchObject({ title: 'tachyon #326 merged', severity: 'info' })
    expect(mk({ kind: 'pr.checks_failed', severity: 'error', title: 'x', subjects: [{ kind: 'pr', ref: 'github:o/r#9', label: 'o/r#9' }] })).toMatchObject({ title: 'r #9 checks failed', severity: 'error' })
    expect(mk({ kind: 'foo.bar_baz', title: 'foo.bar_baz' }).title).toBe('Foo bar baz')
  })
  it('prefers the server display title and source label without changing the source id', async () => {
    const { toAlert } = await import('./adapt')
    const base = { alert_id: 'a1', title: 'approval.requested', source: 'approvals', count: 1, severity: 'warning', kind: 'approval', icon: 'bell', rule_id: 'r', needs_you: true, summary: '', coalesce_key: '', last_at_ms: 1, primary: null } as never
    expect(toAlert({ ...(base as object), display_title: 'Needs your decision: Deploy', source_label: 'Approvals' } as never)).toMatchObject({ title: 'Needs your decision: Deploy', source: 'approvals', sourceLabel: 'Approvals' })
    expect(toAlert(base)).toMatchObject({ title: 'approval.requested', sourceLabel: undefined })
  })
})
