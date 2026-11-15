/** @vitest-environment jsdom */
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { ForgeClient } from '../client'
import { STREAM_RELOAD_MS, usePendingPrs } from './hooks'

class FakeSource {
  static last: FakeSource
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners = new Map<string, () => void>()
  closed = false
  constructor(public url: string) { FakeSource.last = this }
  addEventListener(t: string, f: () => void) { this.listeners.set(t, f) }
  close() { this.closed = true }
}
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals() })

it('reloads when the forge stream reports a change and polls only while the stream is down', async () => {
  vi.useFakeTimers()
  vi.stubGlobal('EventSource', FakeSource)
  const listPendingPrs = vi.fn(async () => [])
  const client = { listPendingPrs } as unknown as ForgeClient
  const { unmount } = renderHook(() => usePendingPrs(client, { pollMs: 60_000, streamUrl: '/forge/stream' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(listPendingPrs).toHaveBeenCalledTimes(1)
  expect(FakeSource.last.url).toBe('/forge/stream')
  act(() => FakeSource.last.onopen?.())
  await act(async () => { await vi.advanceTimersByTimeAsync(180_000) })
  expect(listPendingPrs).toHaveBeenCalledTimes(1) // live and quiet: no poll
  act(() => { for (let i = 0; i < 5; i++) FakeSource.last.listeners.get('pr.updated')?.() })
  await act(async () => { await vi.advanceTimersByTimeAsync(STREAM_RELOAD_MS) })
  expect(listPendingPrs).toHaveBeenCalledTimes(2) // a burst is one reload
  act(() => FakeSource.last.onerror?.())
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
  expect(listPendingPrs).toHaveBeenCalledTimes(3) // stream down: the poll is back
  unmount()
  expect(FakeSource.last.closed).toBe(true)
})
