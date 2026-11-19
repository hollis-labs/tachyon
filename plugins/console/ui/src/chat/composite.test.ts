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

  it('a late first listing is used by an app push that arrives before the Tether side pushes', async () => {
    let appPush: (t: ChatThread[]) => void = () => undefined
    let release: (t: ChatThread[]) => void = () => undefined
    const slow = new Promise<ChatThread[]>((r) => { release = r })
    const primary = { listThreads: () => slow, subscribeThreads: () => () => undefined } as unknown as ChatClient
    const app = stub([th('app', 3)], (l) => { appPush = l })
    const c = createCompositeChatClient(primary, app)
    let last: ChatThread[] = []
    const listing = c.listThreads()
    c.subscribeThreads((t) => { last = t })
    release([th('t1', 2)])
    await listing
    appPush([th('app', 5)])
    expect(last.map((t) => t.id)).toEqual(['app', 't1'])
  })
})
