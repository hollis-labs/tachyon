/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { hostFetch } from '@tachyon/host-sdk'
import { createTetherChatClient, messagesFor, replyTarget, capability, type Turn } from './tether'
import { mergeChatAgents } from './fleet'
import { chatRecipient, type ChatAgent } from './roster'
import { ThreadView } from './ThreadView'
import { createMockChatClient } from '../testkit/chat'

vi.mock('@tachyon/host-sdk', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tachyon/host-sdk')>()),
  hostFetch: vi.fn(),
  useDataFreshness: () => [],
  refreshPageData: async () => undefined,
  dataRefreshRegistry: () => ({ subscribe: () => () => undefined }),
}))
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})
const agent: ChatAgent = {
  launch_id: 'pm-chat',
  agent_id: 'pm',
  display_name: 'Portfolio Manager',
  channel: 'owner',
  session: {
    id: 'current',
    state: 'running',
    at_turn_boundary: true,
    last_activity_at: '2026-10-17T10:00:00Z',
  },
}
const turn: Turn = {
  item_id: 't1',
  agent_id: 'pm',
  session_id: 'current',
  turn_id: 'turn-1',
  kind: 'terminal',
  content: 'Ready for the next step',
  created_at: '2026-10-17T10:00:00Z',
  revision: 3,
  queue_sequence: 4,
  state: 'presented',
  delivery_state: 'delivered',
  replyable: true,
  source_message: { origin: 'routed', attribution: { launch_id: 'pm-chat', runtime: 'claude' } },
}

describe('Tether chat routing', () => {
  it('uses only current-session replyable turns; never replies to a stale session or a publication', () => {
    expect(replyTarget([turn], agent)?.item_id).toBe('t1')
    expect(replyTarget([{ ...turn, session_id: 'old' }], agent)).toBeUndefined()
    expect(replyTarget([{ ...turn, source_message: { origin: 'publication' } }], agent)).toBeUndefined()
    expect(replyTarget([{ ...turn, kind: 'approval' }], agent)).toBeUndefined()
    expect(
      capability({ ...agent, session: { ...agent.session!, at_turn_boundary: false } }, []).canSend,
    ).toBe(false)
    expect(capability({ ...agent, session: { ...agent.session!, state: 'completed' } }, [turn]).canSend).toBe(
      true,
    )
  })
  it('keeps launch identity separate and maps source metadata and operator replies', () => {
    const items = [
      {
        ...turn,
        resolution: { resolution_id: 'r1', response_text: 'Go ahead', resolved_at: '2026-10-17T10:01:00Z' },
      },
      { ...turn, item_id: 'other', source_message: { attribution: { launch_id: 'other-launch' } } },
    ]
    const messages = messagesFor(agent, items, [])
    expect(messages.map((m) => m.from)).toEqual(['agent', 'operator'])
    expect(messages[0].info).toMatchObject({
      transport: 'tether-chat',
      runtime: 'claude',
      launchId: 'pm-chat',
      sessionId: 'current',
    })
  })
  it('keeps distinct initiated messages and turn replies even when their text matches', () => {
    const items = [
      {
        ...turn,
        resolution: { resolution_id: 'r1', response_text: 'hello', resolved_at: '2026-10-17T10:01:00Z' },
      },
    ]
    const messages = messagesFor(agent, items, [
      {
        key: 'sent',
        launch_id: agent.launch_id,
        body: 'hello',
        at: Date.parse('2026-10-17T10:00:30Z'),
        status: 'sent',
        session_id: 'current',
      },
    ])
    expect(messages.filter((m) => m.from === 'operator').map((m) => m.id)).toEqual(['send:sent', 'reply:t1'])
  })
  it('adds configured chat agents without inventing live processes, and matches an exact session once', () => {
    const rows = mergeChatAgents([], [{ ...agent, session: { ...agent.session!, state: 'completed' } }])
    expect(rows[0]).toMatchObject({ pid: 0, state: 'unknown', chat: { launch_id: 'pm-chat' } })
    const existing = { ...rows[0], id: 'process-1', sessionId: 'current', chat: undefined }
    expect(mergeChatAgents([existing], [agent])).toMatchObject([
      { id: 'process-1', chat: { launch_id: 'pm-chat' } },
    ])
  })
  it('joins the unique managed process even when its provider session id differs', () => {
    const process = {
      ...mergeChatAgents([], [agent])[0],
      id: 'process',
      chat: undefined,
      sessionId: 'provider-session',
      cwd: '/home/operator/.tether-worker/workspaces/current/workroot',
      pid: 123,
    }
    expect(mergeChatAgents([process], [agent])).toMatchObject([
      { id: 'process', pid: 123, chat: { launch_id: 'pm-chat' } },
    ])
    expect(mergeChatAgents([process, { ...process, id: 'other' }], [agent])).toHaveLength(3)
    expect(mergeChatAgents([{ ...process, cwd: '/tmp/current/workroot' }], [agent])).toHaveLength(2)
  })
  it('refreshes both sources before replying with revision CAS and does not start another session', async () => {
    const calls: string[] = []
    vi.mocked(hostFetch).mockImplementation(async (input, init) => {
      const url = String(input)
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      const body = url.endsWith('/agents')
        ? { agents: [agent] }
        : url.endsWith('/inbox')
          ? { pending: [turn], history: [] }
          : url.includes('/outbox')
            ? []
            : { ...turn, delivery_state: 'queued' }
      if (url.endsWith('/reply'))
        expect(JSON.parse(String(init?.body))).toEqual({
          expected_revision: 3,
          action: 'respond',
          response_text: 'hello',
        })
      return new Response(JSON.stringify(body), { status: 200 })
    })
    const client = createTetherChatClient(createMockChatClient())
    expect((await client.send(chatRecipient(agent).address, 'hello')).delivery).toBe('queued')
    expect(calls.some((c) => c.endsWith('/items/t1/reply'))).toBe(true)
    expect(calls.some((c) => c.endsWith('/send'))).toBe(false)
    for (const [url, init] of vi
      .mocked(hostFetch)
      .mock.calls.filter(([u]) => String(u).endsWith('/agents') || String(u).endsWith('/inbox')))
      expect(new Headers(init?.headers).get('Cache-Control'), String(url)).toBe('no-cache')
  })
  it('does not send when fresh verification returns a stale fallback', async () => {
    vi.mocked(hostFetch).mockImplementation(
      async (input) =>
        new Response(
          JSON.stringify(
            String(input).endsWith('/agents') ? { agents: [agent] } : { pending: [turn], history: [] },
          ),
          { headers: { 'X-Tachyon-Cache': 'stale' } },
        ),
    )
    const client = createTetherChatClient(createMockChatClient())
    await expect(client.send(chatRecipient(agent).address, 'hello')).rejects.toThrow('Nothing was sent')
    expect(vi.mocked(hostFetch).mock.calls.every(([, init]) => init?.method !== 'POST')).toBe(true)
  })
  it('reuses a failed send key and refuses mid-turn sending', async () => {
    const writes: string[] = []
    let working = false
    vi.mocked(hostFetch).mockImplementation(async (input, init) => {
      const url = String(input)
      if (init?.method === 'POST') {
        writes.push(String(init.body))
        return new Response(JSON.stringify({ message: 'denied' }), { status: 403 })
      }
      return new Response(
        JSON.stringify(
          url.endsWith('/agents')
            ? {
                agents: [
                  { ...agent, session: working ? { ...agent.session!, at_turn_boundary: false } : null },
                ],
              }
            : url.endsWith('/inbox')
              ? { pending: [turn], history: [] }
              : [],
        ),
      )
    })
    const client = createTetherChatClient(createMockChatClient())
    const id = chatRecipient(agent).address
    await expect(client.send(id, 'hello')).rejects.toThrow('denied')
    await expect(client.send(id, 'hello')).rejects.toThrow('denied')
    expect(JSON.parse(writes[0]).idempotency_key).toBe(JSON.parse(writes[1]).idempotency_key)
    working = true
    // A different-session pending turn cannot make a mid-turn session replyable.
    agent.session!.id = 'new-current'
    await expect(client.send(id, 'next')).rejects.toThrow('working')
    agent.session!.id = 'current'
    expect(writes).toHaveLength(2)
  })
})

it('copies exact message text and shows a formatted message-info dialog', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  const m = messagesFor(agent, [turn], [])[0]
  render(<ThreadView messages={[m]} loading={false} hasMore={false} onLoadOlder={() => undefined} />)
  fireEvent.click(screen.getByRole('button', { name: 'Copy message' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(turn.content))
  expect(await screen.findByRole('button', { name: 'Message copied' })).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Download message' })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Message details' }))
  expect(await screen.findByRole('dialog')).toBeTruthy()
  expect(screen.getByText('Tether chat')).toBeTruthy()
  expect(screen.getByText('current')).toBeTruthy()
  expect(screen.getByText('claude')).toBeTruthy()
})
