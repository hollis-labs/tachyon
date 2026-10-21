/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { MemoryRouter } from 'react-router-dom'
import { AutolinkProvider } from '../autolink'
import { ChatPanel, ChatProvider } from '.'
import { applyReplyFilter, hiddenReplies, isForSomeoneElse } from './replyFilter'
import { messagesFor, replyAudience, type Turn } from './tether'
import type { ChatAgent } from './roster'
import type { ChatClient, ChatMessage, ChatThread, ReplyAudience } from './types'

beforeEach(() => {
  Element.prototype.scrollTo ??= () => undefined
  try { localStorage.clear() } catch { /* storage may be unavailable */ }
})
afterEach(cleanup)

// The routed-message metadata Tether stamps (docs/reply-audience.md, fixtures on tether#490).
const turn = (classes: string | string[] | undefined, over: Partial<Turn> = {}): Turn => ({
  item_id: 't', agent_id: 'pm', session_id: 's', turn_id: 'x', kind: 'terminal', content: 'reply',
  created_at: '2026-10-21T10:00:00Z', revision: 1, queue_sequence: 1, state: 'presented', delivery_state: 'delivered',
  source_message: { attribution: { launch_id: 'pm-chat', ...(classes === undefined ? {} : { input_classes: classes }) } }, ...over,
})

// Copies of tether docs/fixtures/reply-audience (tether#490). The metadata is what a routed message
// carries, so these run through the same path a real reply takes.
const FIXTURES = join(__dirname, 'fixtures', 'reply-audience')
describe('Tether reply-audience fixtures', () => {
  const names = readdirSync(FIXTURES).filter((n) => n.endsWith('.json') && n !== 'index.json').sort()
  it('covers the whole contract set', () => {
    expect(names.length).toBeGreaterThanOrEqual(10)
  })
  for (const name of names) {
    const fx = JSON.parse(readFileSync(join(FIXTURES, name), 'utf8')) as { event: { audience: ReplyAudience }; message_metadata: Record<string, string> }
    // Replies to me hides a reply only when Tether says it was written for an agent or the system.
    const want: ReplyAudience = fx.message_metadata.input_classes === 'operator' ? 'operator' : fx.message_metadata.input_classes === 'agent' ? 'agent' : fx.message_metadata.input_classes === 'system' ? 'system' : 'unknown'
    it(`${name}: message metadata`, () => {
      const t = turn(undefined, { source_message: { attribution: fx.message_metadata as never } })
      expect(replyAudience(t)).toBe(want)
      expect(isForSomeoneElse({ id: 'x', threadId: 't', from: 'agent', audience: replyAudience(t), body: '', at: 1, tools: [] })).toBe(name === 'agent.json' || name === 'system.json')
    })
    it(`${name}: event payload`, () => {
      const classes = (fx.event as { input_classes?: string[] }).input_classes
      const t = turn(classes)
      expect(replyAudience(t)).toBe(want)
    })
  }
})

describe('replyAudience', () => {
  it('reads proven classes and treats everything else as unknown', () => {
    expect(replyAudience(turn('operator'))).toBe('operator')
    expect(replyAudience(turn(['operator']))).toBe('operator')
    expect(replyAudience(turn('agent'))).toBe('agent')
    expect(replyAudience(turn('system'))).toBe('system')
    // The unknown fixtures all carry empty input_classes; older replies carry none at all.
    expect(replyAudience(turn(''))).toBe('unknown')
    expect(replyAudience(turn(undefined))).toBe('unknown')
    // A vocabulary that grows later is ignored, not guessed at.
    expect(replyAudience(turn('mixed'))).toBe('unknown')
    expect(replyAudience(turn('robot'))).toBe('unknown')
  })
  it('never infers from the text, the channel or the owner-inbox destination', () => {
    const t = turn(undefined, { content: 'Replying to you, Chrispian', source_message: { channel: 'owner-inbox', attribution: { launch_id: 'pm-chat', audience: 'operator' } } })
    expect(replyAudience(t)).toBe('unknown')
  })
})

describe('applyReplyFilter', () => {
  const m = (id: string, from: ChatMessage['from'], audience?: ReplyAudience): ChatMessage => ({ id, threadId: 't', from, audience, body: id, at: 1, tools: [] })
  const all = [m('mine', 'agent', 'operator'), m('theirs', 'agent', 'agent'), m('sys', 'agent', 'system'), m('old', 'agent', 'unknown'), m('mail', 'agent'), m('me', 'operator'), m('note', 'system')]
  it('shows everything under All', () => {
    expect(applyReplyFilter(all, 'all')).toHaveLength(7)
  })
  it('hides only replies Tether says were for an agent or the system; unknown replies stay', () => {
    expect(applyReplyFilter(all, 'mine').map((x) => x.id)).toEqual(['mine', 'old', 'mail', 'me', 'note'])
  })
  it('counts what is hidden', () => {
    expect(hiddenReplies(all, 'mine')).toEqual({ hidden: 2 })
    expect(hiddenReplies(all, 'all')).toEqual({ hidden: 0 })
  })
  it('carries the audience from turns into messages', () => {
    const a = { launch_id: 'pm-chat', agent_id: 'pm', display_name: 'PM', channel: 'owner' } as ChatAgent
    const out = messagesFor(a, [turn('operator', { item_id: 'a' }), turn('', { item_id: 'b' })], [])
    expect(out.map((x) => x.audience)).toEqual(['operator', 'unknown'])
  })
})

function client(messages: ChatMessage[], hasMore = false): ChatClient {
  const thread: ChatThread = { id: 't', agent: { address: 'msg://agent/x/pm', label: 'PM' } as ChatThread['agent'], title: 'PM', capability: { mode: 'session', canSend: true }, unread: 0, lastAt: 1 }
  return {
    listThreads: async () => [thread],
    openThread: async () => thread,
    history: async () => ({ messages, hasMore }),
    send: async () => messages[0],
    markRead: async () => undefined,
    subscribe: () => () => undefined,
    subscribeThreads: (l) => { l([thread]); return () => undefined },
  }
}
const reply = (id: string, audience: ReplyAudience | undefined, at: number): ChatMessage => ({ id, threadId: 't', from: 'agent', audience, body: `reply ${id}`, at, tools: [] })
const open = async (c: ChatClient) => {
  render(<MemoryRouter><AutolinkProvider><ChatProvider client={c}><ChatPanel /></ChatProvider></AutolinkProvider></MemoryRouter>)
  await act(async () => { await Promise.resolve() })
  fireEvent.click(await screen.findByText('PM'))
  await act(async () => { await Promise.resolve() })
}

describe('reply filter toggle', () => {
  const messages = [reply('a', 'operator', 1), reply('b', 'agent', 2), reply('c', 'unknown', 3), { ...reply('me', undefined, 4), from: 'operator' as const }]
  it('defaults to All with an accessible, pressed-state toggle', async () => {
    await open(client(messages))
    const toggle = screen.getByRole('button', { name: 'Only show replies to me' })
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
    expect(within(toggle).getByText('All replies')).toBeTruthy()
    expect(screen.getByText('reply a')).toBeTruthy()
    expect(screen.getByText('reply b')).toBeTruthy()
  })
  it('hides replies to other agents, keeps the rest and the operator message, and remembers the choice', async () => {
    await open(client(messages))
    fireEvent.click(screen.getByRole('button', { name: 'Only show replies to me' }))
    expect(screen.getByRole('button', { name: 'Only show replies to me' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByText('Replies to me')).toBeTruthy()
    expect(screen.getByText('reply a')).toBeTruthy()
    expect(screen.queryByText('reply b')).toBeNull()
    expect(screen.getByText('reply c')).toBeTruthy()
    expect(screen.getByText('reply me')).toBeTruthy()
    expect(localStorage.getItem('tachyon.chat.replyFilter')).toBe('mine')
    cleanup()
    await open(client(messages))
    expect(screen.getByRole('button', { name: 'Only show replies to me' }).getAttribute('aria-pressed')).toBe('true')
  })
  it('explains an empty filtered view and offers the way out', async () => {
    await open(client([reply('b', 'agent', 2), reply('c', 'system', 3)], true))
    fireEvent.click(screen.getByRole('button', { name: 'Only show replies to me' }))
    const status = screen.getByRole('status')
    expect(within(status).getByText(/Nothing here besides replies to other agents in what has loaded so far/)).toBeTruthy()
    expect(within(status).getByText(/2 replies to other agents are hidden/)).toBeTruthy()
    expect(within(status).getByText('Load older messages')).toBeTruthy()
    fireEvent.click(within(status).getByText('Show all replies'))
    expect(screen.getByText('reply b')).toBeTruthy()
  })
  it('keeps an incoming reply that is for the operator and drops one that is not', async () => {
    const listeners: Array<(e: import('./types').ChatEvent) => void> = []
    const c = { ...client(messages), subscribe: (_: string, l: (e: import('./types').ChatEvent) => void) => { listeners.push(l); return () => undefined } }
    await open(c)
    fireEvent.click(screen.getByRole('button', { name: 'Only show replies to me' }))
    act(() => listeners.forEach((l) => { l({ type: 'message', message: reply('new-agent', 'agent', 9) }); l({ type: 'message', message: reply('new-mine', 'operator', 10) }) }))
    expect(screen.queryByText('reply new-agent')).toBeNull()
    expect(screen.getByText('reply new-mine')).toBeTruthy()
  })
})
