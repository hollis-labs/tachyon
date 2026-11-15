import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import live from './fixtures/live-sessions.json'
import { AgentsErrorBoundary } from './error-boundary'
import { ActivityTab } from './activity-tab'
import { normalizeSessions } from './data'
import { SessionModal } from './session-modal'
import { SessionBoard, SessionCards, SessionRows } from './sessions-view'
import type { SessionsResponse } from './types'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

// Shapes copied from the live /api/plugins/console/v1/agents API (Go sends null for empty lists, "" for unknown text).
const sessions = normalizeSessions(live as unknown as SessionsResponse).sessions
const body: Record<string, unknown> = {
  events: { events: null, gaps: [], next_before: null },
  'context-series': { clears: [], compactions: [], points: [], warn_pct: 90 },
  tokens: { hour: null, metered: false, session: null, share: null, trend: null },
  history: { sessions: [] },
  settings: { stale_after_s: 900, handoff_prompt: 'x', handoff_timeout_s: 300, nudge_presets: [] },
  collector: { bus_topics: { deploy: 'off', host: 'no_emitter' } },
  bus: { events: null, next_before: null },
}
const stubFetch = () =>
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    const key = Object.keys(body).find((k) => url.includes(`/${k}`)) ?? 'events'
    return Promise.resolve(new Response(JSON.stringify(body[key]), { status: 200 }))
  }))

describe('real payloads', () => {
  it('opens the session modal on every tab for a live session with null lists', async () => {
    stubFetch()
    for (const s of sessions) {
      const { unmount } = render(<MemoryRouter><SessionModal session={s} paused={false} now={Date.now()} onClose={() => undefined} onRestarted={() => undefined} /></MemoryRouter>)
      for (const tab of ['Now', 'Timeline', 'Context', 'History']) {
        screen.getByRole('tab', { name: tab }).click()
        await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
      }
      unmount()
    }
  })

  it('words Right now as the golden: started age, tool and calls this turn; no start when it was not seen', async () => {
    stubFetch()
    const now = Date.parse('2026-11-15T02:00:00Z')
    const base = { ...sessions[0], state: 'working' as const, one_liner: { verb: 'Running', target: 'go test', tool: 'Bash', at: now - 5000, calls_this_turn: 14 } }
    const { unmount } = render(<MemoryRouter><SessionModal session={{ ...base, one_liner: { ...base.one_liner, turn_started_at: now - 120_000 } }} paused={false} now={now} onClose={() => undefined} onRestarted={() => undefined} /></MemoryRouter>)
    expect(await screen.findByText(/Started 2m ago ·/)).toBeTruthy()
    expect(screen.getByText(/14 tool calls this turn/)).toBeTruthy()
    unmount()
    render(<MemoryRouter><SessionModal session={base} paused={false} now={now} onClose={() => undefined} onRestarted={() => undefined} /></MemoryRouter>)
    expect(await screen.findByText(/14 tool calls this turn/)).toBeTruthy()
    expect(screen.queryByText(/Started .* ago/)).toBeNull()
  })

  it('draws the golden tokens table: per-kind share and a trend line, a dash when no agent used that kind', async () => {
    const series = (v: number) => Array.from({ length: 60 }, (_, i) => (i % 5 === 0 ? v : 0))
    body.tokens = { metered: true, hour: { input: 100, output: 50, cache_read: 0, cache_write: 7, tool_calls: 3 }, session: { input: 400, output: 200, cache_read: 0, cache_write: 9, tool_calls: 9 }, share: 0.3, trend: 'up',
      by_kind: { input: { share: 0.26, trend: 'up', series: series(10) }, output: { share: 0.2, trend: 'flat', series: series(5) }, cache_read: { share: null, trend: null, series: series(0) }, cache_write: { share: 0.07, trend: 'down', series: series(1) } } }
    stubFetch()
    render(<MemoryRouter><SessionModal session={sessions[0]} paused={false} now={Date.now()} onClose={() => undefined} onRestarted={() => undefined} /></MemoryRouter>)
    screen.getByRole('tab', { name: 'Context' }).click()
    expect(await screen.findByText('26%')).toBeTruthy()
    expect(screen.getByText('20%')).toBeTruthy()
    expect(screen.getByText('7%')).toBeTruthy()
    expect(screen.getByText('—')).toBeTruthy()
    expect(screen.getByText('Trend')).toBeTruthy()
    expect(screen.queryByText(/Share of all agents/)).toBeNull()
    body.tokens = { hour: null, metered: false, session: null, share: null, trend: null }
  })

  it('renders Rows, Cards and Board for live sessions without an empty provider chip or a blank group', () => {
    render(<><SessionRows sessions={sessions} groupBy="workroot" now={Date.now()} onSelect={() => undefined} /><SessionCards sessions={sessions} now={Date.now()} onSelect={() => undefined} /><SessionBoard sessions={sessions} now={Date.now()} onSelect={() => undefined} /></>)
    expect(screen.getAllByText('No workspace').length).toBeGreaterThan(0)
  })

  it('renders Activity when the bus returns events: null', async () => {
    stubFetch()
    render(<ActivityTab busTick={0} now={Date.now()} />)
    expect(await screen.findByText('No events yet; collecting')).toBeTruthy()
  })

  it('shows an error state instead of a blank page when a view throws', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const Bomb = () => {
      throw new Error('bad payload')
    }
    render(<AgentsErrorBoundary onClose={() => undefined}><Bomb /></AgentsErrorBoundary>)
    expect(screen.getByRole('alert').textContent).toMatch(/could not be shown/)
  })
})
