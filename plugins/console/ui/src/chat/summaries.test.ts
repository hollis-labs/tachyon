import { describe, expect, it, vi } from 'vitest'
import { withChatApiSummaries } from './summaries'
import type { ChatClient, ChatThread } from './types'

const row = (over: Partial<ChatThread> = {}): ChatThread => ({
  id: 'chat://tether/launch/pm', title: 'PM', unread: 1, lastAt: 10, lastPreview: 'old',
  agent: { address: 'chat://tether/launch/pm', label: 'PM', kind: 'agent', chatLaunchId: 'pm' },
  capability: { mode: 'session' }, ...over,
})
const base = (threads: ChatThread[]): ChatClient => ({
  listThreads: async () => threads,
  openThread: vi.fn(), history: vi.fn(), send: vi.fn(),
  markRead: vi.fn(async () => undefined),
  subscribe: vi.fn(() => () => undefined),
  subscribeThreads: vi.fn(() => () => undefined),
})
const api = [
  { thread_ref: 'tether:old', title: 'Older', agent: { agent_id: 'pm' }, unread: 330, last_at: 100, last_preview: 'older words' },
  { thread_ref: 'tether:new', title: 'Newer', agent: { agent_id: 'pm' }, unread: 2, last_at: 500, last_preview: 'newest words', blocked_on_user: 1 },
  { thread_ref: 'tether:gone', agent: { agent_id: 'pm' }, unread: 9, last_at: 900, archived: true },
]

describe('withChatApiSummaries', () => {
  it('sums unread, flags needs-you, and takes preview and time from the newest session', async () => {
    const [t] = await withChatApiSummaries(base([row()]), async () => api).listThreads()
    expect(t).toMatchObject({ unread: 332, needsYou: true, lastAt: 500, lastPreview: 'newest words' })
    expect(t.sessions?.map((s) => s.ref)).toEqual(['tether:new', 'tether:old'])
  })
  it('leaves the rows alone when the chat API is unavailable', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const [t] = await withChatApiSummaries(base([row()]), async () => { throw new Error('down') }).listThreads()
    expect(t).toMatchObject({ unread: 1, lastPreview: 'old' })
    expect(t.sessions).toBeUndefined()
  })
  it('marks every unread session read on the server', async () => {
    const marked: string[] = []
    const c = withChatApiSummaries(base([row()]), async () => api, async (ref) => void marked.push(ref))
    await c.listThreads()
    await c.markRead('chat://tether/launch/pm')
    expect(marked).toEqual(['tether:new', 'tether:old'])
  })
})

describe('blocked breakdown', () => {
  it('sums approve/answer counts over an agent\'s sessions', async () => {
    const { withChatApiSummaries } = await import('./summaries')
    const base = { listThreads: async () => [{ id: 'a', agent: { address: 'a', label: 'a', kind: 'agent', agentId: 'pm' }, title: 'a', capability: { mode: 'live' }, unread: 0, lastAt: 1 }], subscribeThreads: () => () => undefined } as never
    const c = withChatApiSummaries(base, async () => [{ thread_ref: 't1', agent: { agent_id: 'pm' }, blocked_on_user: 3, blocked_breakdown: { approvals: 2, questions: 1 } }, { thread_ref: 't2', agent: { agent_id: 'pm' }, blocked_on_user: 1, blocked_breakdown: { approvals: 0, questions: 1 } }])
    const [t] = await c.listThreads()
    expect(t.blockedBreakdown).toEqual({ approvals: 2, questions: 2 })
  })
})
