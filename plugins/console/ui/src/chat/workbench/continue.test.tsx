/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import type { ChatThread } from '../types'

// The server side of /threads/{ref}/continue: one flag per session, fixed from the default on first read.
const server = { def: true, rows: new Map<string, boolean>(), failPut: false, gate: null as Promise<void> | null, calls: [] as { m: string; path: string; b?: string }[] }
vi.mock('../roster', () => ({
  CHAT_BASE: '',
  chatFetch: fetch,
  chatJSON: async (path: string, init?: RequestInit) => {
    const m = init?.method ?? 'GET'
    server.calls.push({ m, path, b: init?.body as string | undefined })
    const ref = decodeURIComponent(path.split('/')[2])
    if (m === 'PUT') {
      if (server.gate) await server.gate
      if (server.failPut) throw new Error('Operator access is needed')
      const on = JSON.parse(String(init?.body)).continue as boolean
      server.rows.set(ref, on)
      return { continue: on }
    }
    if (!server.rows.has(ref)) server.rows.set(ref, server.def)
    return { continue: server.rows.get(ref) }
  },
}))
import { CONTINUE_LABEL, Header } from './Header'
import { useContinue } from './continue'

beforeEach(() => {
  server.def = true
  server.rows.clear()
  server.failPut = false
  server.gate = null
  server.calls = []
})
afterEach(cleanup)

const thread = (over: Partial<ChatThread> = {}): ChatThread => ({
  id: 'chat://tether/launch/pm', agent: { address: 'chat://tether/launch/pm', label: 'Planner' } as ChatThread['agent'], title: 'Planner',
  capability: { mode: 'live' }, unread: 0, lastAt: 0, ...over,
})

describe('chat header pin and continue chips', () => {
  it('shows the pin as an icon only, labelled Pinned or Pin', () => {
    const { rerender } = render(<Header thread={thread({ pinned: true })} />)
    const pin = screen.getByRole('button', { name: 'Pinned' })
    expect(pin.textContent).toBe('')
    expect(pin.getAttribute('title')).toBe('Pinned')
    expect(pin.getAttribute('aria-pressed')).toBe('true')
    rerender(<Header thread={thread({ pinned: false })} />)
    expect(screen.getByRole('button', { name: 'Pin' }).getAttribute('aria-pressed')).toBe('false')
  })

  it('shows the continue toggle with its state in aria-pressed, label and tooltip', () => {
    const onToggle = vi.fn()
    const { rerender } = render(<Header thread={thread()} continueOn onToggleContinue={onToggle} />)
    const on = screen.getByRole('button', { name: CONTINUE_LABEL.on })
    expect(on.getAttribute('aria-pressed')).toBe('true')
    expect(on.getAttribute('title')).toBe(CONTINUE_LABEL.on)
    fireEvent.click(on)
    expect(onToggle).toHaveBeenCalledOnce()
    rerender(<Header thread={thread()} continueOn={false} onToggleContinue={onToggle} />)
    const off = screen.getByRole('button', { name: CONTINUE_LABEL.off })
    expect(off.getAttribute('aria-pressed')).toBe('false')
    expect(off.getAttribute('title')).toBe(CONTINUE_LABEL.off)
  })

  it('hides the continue toggle when the session has none', () => {
    render(<Header thread={thread()} continueOn={null} />)
    expect(screen.queryByRole('button', { name: /^Continue/ })).toBeNull()
  })
})

describe('useContinue', () => {
  it('reads the session flag and persists a toggle for that session', async () => {
    const { result } = renderHook(() => useContinue('tether:s1'))
    await waitFor(() => expect(result.current.on).toBe(true))
    act(() => result.current.toggle())
    expect(result.current.on).toBe(false)
    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(server.calls.at(-1)).toEqual({ m: 'PUT', path: '/threads/tether%3As1/continue', b: JSON.stringify({ continue: false }) })
    // Reopening the session reads the stored value back.
    const again = renderHook(() => useContinue('tether:s1'))
    await waitFor(() => expect(again.result.current.on).toBe(false))
  })

  it('takes the settings default for a new session, and a later change of the default leaves an open session alone', async () => {
    server.def = false
    const first = renderHook(() => useContinue('tether:new'))
    await waitFor(() => expect(first.result.current.on).toBe(false))
    server.def = true
    const reopened = renderHook(() => useContinue('tether:new'))
    await waitFor(() => expect(reopened.result.current.on).toBe(false))
    const later = renderHook(() => useContinue('tether:later'))
    await waitFor(() => expect(later.result.current.on).toBe(true))
  })

  it('rolls back and reports when the change is refused', async () => {
    const { result } = renderHook(() => useContinue('tether:s1'))
    await waitFor(() => expect(result.current.on).toBe(true))
    server.failPut = true
    act(() => result.current.toggle())
    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.on).toBe(true)
    expect(result.current.error).toBe('Operator access is needed')
  })

  it('drops a toggle answer that arrives after another session was opened', async () => {
    server.rows.set('tether:b', true)
    const { result, rerender } = renderHook(({ ref }) => useContinue(ref), { initialProps: { ref: 'tether:a' } })
    await waitFor(() => expect(result.current.on).toBe(true))
    let release!: () => void
    server.gate = new Promise((r) => { release = r })
    act(() => result.current.toggle()) // A goes off; its PUT is held
    rerender({ ref: 'tether:b' })
    await waitFor(() => expect(result.current.on).toBe(true)) // B's own value
    expect(result.current.busy).toBe(false)
    await act(async () => { release() })
    expect(result.current.on).toBe(true) // A's answer (false) did not land on B
    expect(server.rows.get('tether:a')).toBe(false)
  })

  it('drops a refusal that arrives after another session was opened, so B shows no error', async () => {
    const { result, rerender } = renderHook(({ ref }) => useContinue(ref), { initialProps: { ref: 'tether:a' } })
    await waitFor(() => expect(result.current.on).toBe(true))
    let release!: () => void
    server.gate = new Promise((r) => { release = r })
    server.failPut = true
    act(() => result.current.toggle())
    rerender({ ref: 'tether:b' })
    await waitFor(() => expect(result.current.on).toBe(true))
    await act(async () => { release() })
    expect(result.current.error).toBe('')
  })

  it('clears the error notice on dismiss', async () => {
    const { result } = renderHook(() => useContinue('tether:s1'))
    await waitFor(() => expect(result.current.on).toBe(true))
    server.failPut = true
    act(() => result.current.toggle())
    await waitFor(() => expect(result.current.error).toBe('Operator access is needed'))
    act(() => result.current.dismiss())
    expect(result.current.error).toBe('')
  })

  it('has no toggle for a thread that is not a Tether session', async () => {
    const { result } = renderHook(() => useContinue('app:t1'))
    await act(async () => undefined)
    expect(result.current.on).toBeNull()
    expect(server.calls).toEqual([])
  })
})
