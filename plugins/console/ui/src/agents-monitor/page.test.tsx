import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import live from './fixtures/live-sessions.json'
import { AgentsPage } from './page'
import type { SessionsResponse } from './types'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const list = live as unknown as SessionsResponse
const ENDED = 'ended-session-id'
const stubFetch = () =>
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = String(input)
    const json = (b: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(b), { status }))
    if (url.endsWith('/agents/sessions')) return json(list)
    if (url.endsWith(`/sessions/${ENDED}`)) return json({ ...list.sessions[0], id: ENDED, agent: { ...list.sessions[0].agent, name: 'ended-builder' }, state: 'ended' })
    if (url.endsWith('/sessions/unknown-id')) return json({ code: 'not_found', message: 'No such session.' }, 404)
    if (url.includes('/settings')) return json({ stale_after_s: 900, handoff_prompt: 'x', handoff_timeout_s: 300, nudge_presets: [] })
    if (url.includes('/history')) return json({ sessions: [] })
    if (url.includes('/context-series')) return json({ clears: [], compactions: [], points: [], warn_pct: 90 })
    if (url.includes('/tokens')) return json({ hour: null, metered: false, session: null, share: null, trend: null })
    return json({ events: null, gaps: [], next_before: null })
  }))
const at = (path: string) => render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/agents/:session?" element={<AgentsPage />} /></Routes></MemoryRouter>)

describe('Agents page', () => {
  it('opens a session that ended (it is no longer in the list) from its link', async () => {
    stubFetch()
    at(`/agents/${ENDED}`)
    const dialog = await screen.findByRole('dialog')
    expect(dialog.textContent).toContain('ended-builder')
    expect(dialog.textContent).toContain('Ended')
  })

  it('says so when the linked session does not exist, and goes back to the list', async () => {
    stubFetch()
    at('/agents/unknown-id')
    expect((await screen.findAllByText('Session not found')).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: 'Back to sessions' }))
    await waitFor(() => expect(screen.queryAllByText('Session not found').length).toBe(0))
  })

  it('j moves focus to the list from anywhere on the page', async () => {
    stubFetch()
    at('/agents')
    await waitFor(() => expect(document.querySelector('[data-session-row]')).toBeTruthy())
    fireEvent.keyDown(window, { key: 'j' })
    expect((document.activeElement as HTMLElement).hasAttribute('data-session-row')).toBe(true)
  })
})
