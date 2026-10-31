import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NudgeControl } from './controls'
import { toTimeline } from './session-events'
import type { Session } from './types'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const session = {
  id: 'sess_work', agent: { urn: null, name: 'builder', logical_agent_id: null }, capabilities: { nudge: true, interrupt: { available: true, reason: null }, restart: true },
} as unknown as Session

const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function open() {
  render(<NudgeControl session={session} paused={false} live onSent={vi.fn()} />)
  fireEvent.click(screen.getByRole('button', { name: /^Nudge/ }))
}

describe('Nudge', () => {
  it('sends once on a double click, with the typed text', async () => {
    const calls: { url: string; body: string }[] = []
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      if (init?.method === 'POST') { calls.push({ url, body: String(init.body) }); return new Promise((r) => setTimeout(() => r(new Response(JSON.stringify({ request_id: 'r', state: 'delivered' }), { status: 202 })), 20)) }
      return json(200, { nudge_presets: ['Continue'] })
    }))
    open()
    fireEvent.change(await screen.findByLabelText('Nudge or steer'), { target: { value: 'run the tests' } })
    const send = screen.getByRole('button', { name: /^Send/ })
    fireEvent.click(send)
    fireEvent.click(send)
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(JSON.parse(calls[0].body)).toMatchObject({ text: 'run the tests', interrupt: false })
    expect(calls[0].url).toContain('/sessions/sess_work/nudge')
  })

  it('keeps the draft and offers Send without interrupt when the interrupt is refused', async () => {
    const bodies: { interrupt: boolean }[] = []
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => {
      if (init?.method !== 'POST') return json(200, { nudge_presets: [] })
      const b = JSON.parse(String(init.body))
      bodies.push(b)
      return b.interrupt ? json(409, { code: 'interrupt_unsupported', message: 'This provider cannot interrupt' }) : json(202, { request_id: 'r', state: 'delivered' })
    }))
    open()
    const box = (await screen.findByLabelText('Nudge or steer')) as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'wrap up' } })
    fireEvent.click(screen.getByLabelText('Interrupt the current turn'))
    fireEvent.click(screen.getByRole('button', { name: /^Send/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Send without interrupt' }))
    await waitFor(() => expect(bodies).toHaveLength(2))
    expect(bodies.map((b) => b.interrupt)).toEqual([true, false])
  })

  it('is disabled with a visible reason until the backend controls are approved', () => {
    render(<NudgeControl session={session} paused={false} live={false} onSent={vi.fn()} />)
    const b = screen.getByRole('button', { name: /^Nudge/ }) as HTMLButtonElement
    expect(b.disabled).toBe(true)
    expect(b.title).toMatch(/waiting for backend approval/)
  })

  it('is disabled with a visible reason while reconnecting', () => {
    render(<NudgeControl session={session} paused live onSent={vi.fn()} />)
    const b = screen.getByRole('button', { name: /^Nudge/ }) as HTMLButtonElement
    expect(b.disabled).toBe(true)
    expect(b.title).toBe('Controls are paused while reconnecting')
  })
})

describe('toTimeline', () => {
  it('puts a collection gap where it happened and keeps grouped tool runs as one row', () => {
    const now = Date.parse('2026-10-30T12:00:00Z')
    const rows = toTimeline(
      {
        events: [
          { id: 'e2', type: 'message', at: now - 1000, summary: 'Hello' },
          { type: 'tool_run', run_id: 'r1', started_at: now - 600000, summary: 'read x2', calls: 2, events: [{ id: 'c1', type: 'tool', at: now - 600000, summary: 'Reading a' }] },
        ],
        gaps: [{ start: now - 300000, end: now - 200000, reason: 'tachyon_offline', backfilled: false }],
        next_before: null,
      },
      now,
    )
    expect(rows.map((r) => r.id)).toEqual(['e2', 'gap0', 'r1'])
    expect(rows[2].facet).toBe('tools')
  })
})
