/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { ChatProvider } from './context'
import { useChatThread } from './hooks'
import type { ChatClient, ChatEvent } from './types'

afterEach(() => { cleanup(); vi.useRealTimers() })

const msg = (id: string, at: number) => ({ type: 'message', message: { id, threadId: 't', from: 'agent', body: id, at, tools: [] } }) as unknown as ChatEvent

// Opening a chat: the live sync re-emits its newest page oldest-first before history lands. That used to send
// one POST /read per agent message in the page (about ten at once); it is one per batch now.
it('marks a thread read once for a burst of newer agent messages, not once per message', async () => {
  vi.useFakeTimers()
  let emit: (e: ChatEvent) => void = () => undefined
  const markRead = vi.fn(async () => undefined)
  const client = {
    history: () => new Promise(() => {}), // history has not landed yet
    subscribe: (_id: string, l: (e: ChatEvent) => void) => { emit = l; return () => undefined },
    markRead,
  } as unknown as ChatClient
  const wrapper = ({ children }: { children: ReactNode }) => <ChatProvider client={client}>{children}</ChatProvider>
  renderHook(() => useChatThread('t'), { wrapper })
  act(() => { for (let i = 1; i <= 10; i++) emit(msg(`m${i}`, i * 1000)) })
  await act(async () => { await vi.advanceTimersByTimeAsync(1) })
  expect(markRead).toHaveBeenCalledTimes(1)
  // A later, newer agent message marks read again; a re-emit of the same page does not.
  act(() => { for (let i = 1; i <= 10; i++) emit(msg(`m${i}`, i * 1000)) })
  await act(async () => { await vi.advanceTimersByTimeAsync(1) })
  expect(markRead).toHaveBeenCalledTimes(1)
  act(() => emit(msg('m11', 11000)))
  await act(async () => { await vi.advanceTimersByTimeAsync(1) })
  expect(markRead).toHaveBeenCalledTimes(2)
})
