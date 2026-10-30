import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ActivityTab, toBusTimeline, type BusEvent } from './activity-tab'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const NOW = Date.parse('2026-10-30T12:00:00Z')
const ev = (over: Partial<BusEvent> & { id: string; seq: number }): BusEvent => ({ at: NOW - 1000, type: 'deploy', subject: 'Deployed tachyon', actor: 'archie', app: 'tachyon', severity: 'info', refs: [], card: null, ...over })
const page = (events: BusEvent[], next: number | null) => Promise.resolve(new Response(JSON.stringify({ events, next_before: next }), { status: 200 }))

describe('Activity', () => {
  it('colours only failures and maps types to facets', () => {
    const rows = toBusTimeline([ev({ id: 'a', seq: 3 }), ev({ id: 'b', seq: 2, type: 'failure', severity: 'error' }), ev({ id: 'c', seq: 1, type: 'host' })], NOW)
    expect(rows.map((r) => [r.facet, r.tone])).toEqual([['deploys', 'neutral'], ['failures', 'blocked'], ['other', 'neutral']])
  })

  it('shows the empty state, then pages older events in with Show more', async () => {
    const urls: string[] = []
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      urls.push(url)
      return url.includes('before_seq=5') ? page([ev({ id: 'old', seq: 4, subject: 'Older deploy' })], null) : page([ev({ id: 'new', seq: 6, subject: 'Newer deploy' })], 5)
    }))
    render(<ActivityTab busTick={0} now={NOW} />)
    expect(await screen.findByText(/Newer deploy/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Show 50 more' }))
    expect(await screen.findByText(/Older deploy/)).toBeTruthy()
    expect(urls.some((u) => u.includes('before_seq=5'))).toBe(true)
    await waitFor(() => expect(screen.queryByRole('button', { name: /Show/ })).toBeNull())
  })

  it('says collecting when there are no events', async () => {
    vi.stubGlobal('fetch', vi.fn(() => page([], null)))
    render(<ActivityTab busTick={0} now={NOW} />)
    expect(await screen.findByText('No events yet; collecting')).toBeTruthy()
  })
})
