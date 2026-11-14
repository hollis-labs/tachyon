import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SeatsTab, type Seat } from './seats-tab'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const NOW = Date.parse('2026-11-14T12:00:00Z')
const seats: Seat[] = [
  { agent_urn: 'msg://agent/agent-mux/agt_b', handle: '@builder-1', name: 'Builder One', effective: 'blocked',
    declared: { state: 'blocked', reason: 'needs the deploy token', ref: 'task:CW-20261114-0099', owner: '@chrispian', expires_at: '2026-11-14T14:00:00Z', set_by: 'msg://agent/agent-mux/agt_b', at: '2026-11-14T11:50:00Z' },
    live: { session_id: 's-b', state: 'waiting', needs_you: 'approval', last_activity_at: NOW - 120_000 } },
  { agent_urn: 'msg://agent/agent-mux/agt_w', handle: '', name: 'Worker', effective: 'undeclared', declared: null,
    live: { session_id: 's-w', state: 'working', needs_you: null, last_activity_at: NOW - 5_000 } },
  { agent_urn: 'msg://agent/agent-mux/agt_i', handle: '@idle', name: '@idle', effective: 'available',
    declared: { state: 'available', reason: '', ref: '', owner: '', expires_at: null, set_by: 'x', at: '2026-11-14T10:00:00Z' }, live: null },
]
const show = (body: unknown) => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))))
  return render(<MemoryRouter><SeatsTab busTick={0} now={NOW} /></MemoryRouter>)
}

describe('Seats', () => {
  it('shows each seat: declared state, reason, owner, ref and its live session', async () => {
    show({ seats })
    const cards = within(await screen.findByRole('list', { name: 'Seats' })).getAllByRole('listitem')
    expect(cards.map((c) => c.getAttribute('data-seat'))).toEqual(seats.map((s) => s.agent_urn))
    const b = cards[0]
    expect(b.textContent).toContain('Builder One')
    expect(b.textContent).toContain('@builder-1')
    expect(b.textContent).toContain('blocked')
    expect(b.textContent).toContain('needs the deploy token')
    expect(b.textContent).toContain('waiting on @chrispian')
    expect(b.textContent).toContain('needs you: approval')
    expect(b.querySelector('a[data-ref-kind="torque.task"]')?.getAttribute('href')).toBe('/tasks/CW-20261114-0099')
    expect(within(b).getByRole('link', { name: 'session waiting' }).getAttribute('href')).toBe('/agents/s-b')
    expect(cards[1].textContent).toContain('no status set')
    expect(cards[2].textContent).toContain('no live session')
    expect(cards[2].textContent).toContain('available')
  })

  it('says when no seat has a status or a session', async () => {
    show({ seats: [] })
    expect(await screen.findByText('No seat has declared a status or has a live session.')).toBeTruthy()
  })
})
