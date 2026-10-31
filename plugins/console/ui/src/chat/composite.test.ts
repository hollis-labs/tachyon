import { describe, expect, it } from 'vitest'
import { createCompositeChatClient } from './composite'
import type { ChatClient, ChatThread } from './types'

const th = (id: string, lastAt: number): ChatThread => ({ id, agent: { address: id, label: id, kind: 'agent' }, title: id, capability: { mode: 'live' }, unread: 0, lastAt }) as ChatThread
const stub = (threads: ChatThread[], onSub?: (l: (t: ChatThread[]) => void) => void): ChatClient => ({ listThreads: async () => threads, subscribeThreads: (l: (t: ChatThread[]) => void) => { onSub?.(l); return () => undefined } }) as unknown as ChatClient

describe('composite thread list', () => {
  it('an app push does not drop the Tether threads when the Tether side has not pushed yet', async () => {
    const c = createCompositeChatClient(stub([th('t1', 2), th('t2', 1)]), stub([th('app', 3)], (l) => l([th('app', 4)])))
    await c.listThreads()
    let last: ChatThread[] = []
    c.subscribeThreads((t) => { last = t })
    expect(last.map((t) => t.id)).toEqual(['app', 't1', 't2'])
  })
})
