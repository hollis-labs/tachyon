import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NeedsYouPage, type NeedsYouResult } from './page'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const ok = { ok: true }
const result = (over: Partial<NeedsYouResult>): NeedsYouResult => ({ items: [], sources: { asks: ok, mail: ok, statuses: ok }, checked_at: new Date().toISOString(), ...over })
const fetchOf = (body: NeedsYouResult) => vi.fn((_url: string) => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })))
const show = (f: ReturnType<typeof fetchOf>) => render(<MemoryRouter><NeedsYouPage fetch={f} /></MemoryRouter>)

describe('Needs you', () => {
  it('lists what waits on you, critical first, with why, kind, sender and ref', async () => {
    const since = new Date(Date.now() - 90 * 60_000).toISOString()
    const f = fetchOf(result({ items: [
      { id: 'ask:a', source: 'ask', kind: 'approval_request', why: 'reply', title: 'Deploy tachyon now?', from: '@builder-1', since, critical: true, message_id: 'a' },
      { id: 'mail:b', source: 'mail', kind: 'decision', why: 'reply', title: 'Keep or patch the dialog?', from: '@chief', ref: 'task:CW-20261114-0027', since, critical: false, message_id: 'b' },
      { id: 'status:c', source: 'status', kind: 'blocked', why: 'unblock', title: 'needs the deploy token', from: '@photon-tether', since, critical: false },
    ] }))
    show(f)
    const list = await screen.findByRole('list', { name: 'Needs you' })
    const rows = within(list).getAllByRole('listitem')
    expect(rows.map((r) => r.getAttribute('data-item'))).toEqual(['ask:a', 'mail:b', 'status:c'])
    expect(rows[0].textContent).toContain('Critical')
    expect(rows[0].textContent).toContain('Waiting on your reply')
    expect(rows[0].textContent).toContain('approval request')
    expect(rows[0].textContent).toContain('2h')
    expect(rows[1].querySelector('a[data-ref-kind="torque.task"]')?.getAttribute('href')).toBe('/tasks/CW-20261114-0027')
    expect(rows[2].textContent).toContain('Blocked on you')
    expect(rows[2].textContent).not.toContain('Critical')
    expect(String(f.mock.calls[0][0])).toContain('/needs-you')
  })

  it('links chat threads and the approvals queue to where you act on them', async () => {
    const since = new Date().toISOString()
    show(fetchOf(result({ items: [
      { id: 'approvals:queue', source: 'approvals', kind: 'approval', why: 'decide', title: '3 approvals waiting in the approvals queue', from: 'Tachyon', since, critical: true, detail: '1 high risk', link: '/approvals' },
      { id: 'chat:tether:s1', source: 'chat', kind: 'approval', why: 'reply', title: 'Deploy plan', from: 'Task Ops Manager', since, critical: false, detail: '2 approvals, 1 question', link: '/chat/chat:%2F%2Ftether%2Flaunch%2Ftaskops-manager' },
    ] })))
    const queue = await screen.findByRole('link', { name: '3 approvals waiting in the approvals queue' })
    expect(queue.getAttribute('href')).toBe('/approvals')
    expect(screen.getByRole('link', { name: 'Deploy plan' }).getAttribute('href')).toBe('/chat/chat:%2F%2Ftether%2Flaunch%2Ftaskops-manager')
    const rows = within(screen.getByRole('list', { name: 'Needs you' })).getAllByRole('listitem')
    expect(rows[0].textContent).toContain('Waiting on your decision')
    expect(rows[0].textContent).toContain('1 high risk')
    expect(rows[1].textContent).toContain('2 approvals, 1 question')
  })

  it('says nothing waits only when every source was read', async () => {
    show(fetchOf(result({})))
    expect(await screen.findByText('Nothing is waiting on you.')).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('never passes off a partial list as complete', async () => {
    show(fetchOf(result({ sources: { asks: { ok: false, error: 'Tether pending replies unavailable: tether unreachable' }, mail: { ok: true, truncated: true }, statuses: ok } })))
    expect((await screen.findByRole('alert')).textContent).toContain('may be incomplete: could not read typed asks')
    expect(screen.getByText(/Only the newest items of your mailbox were checked/)).toBeTruthy()
  })

  it('shows a load failure', async () => {
    show(vi.fn(() => Promise.resolve(new Response(JSON.stringify({ code: 'unavailable', message: 'down' }), { status: 503 }))))
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('Could not load the needs-you list'))
  })
})
