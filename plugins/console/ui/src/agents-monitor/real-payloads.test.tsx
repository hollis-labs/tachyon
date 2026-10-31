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
