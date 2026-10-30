/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { AutolinkProvider } from '../../autolink'
import { createMockChatClient, MOCK_CHAT_AGENTS } from '../../testkit/chat'
import { ChatProvider } from '..'
import type { ChatThread } from '../types'
import { ChatPage } from './ChatPage'
import { ChatView } from './ChatView'
import { ConversationView } from './ConversationView'
import { ThreadList } from './ThreadList'
import { resetDraftsForTest } from '../drafts'

vi.mock('../roster', async (orig) => ({
  ...(await orig<typeof import('../roster')>()),
  useChatRoster: () => ({ agents: [{ launch_id: 'l-1', agent_id: 'pm', display_name: 'Portfolio Manager', channel: 'chat', session: null }], error: null }),
}))

beforeEach(() => {
  resetDraftsForTest()
  vi.useFakeTimers()
  Element.prototype.scrollTo ??= () => undefined
  window.requestAnimationFrame = ((cb: FrameRequestCallback) => setTimeout(() => cb(0), 0)) as never
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

const flush = () => act(async () => void (await vi.advanceTimersByTimeAsync(0)))
const thread = (id: string, title: string, over: Partial<ChatThread> = {}): ChatThread => ({
  id, title, unread: 0, lastAt: 1, agent: { address: `msg://agent/x/${id}`, label: title, kind: 'agent' },
  capability: { mode: 'live' }, ...over,
})
const T = [thread('a', 'Alpha', { lastPreview: 'deploy done' }), thread('b', 'Bravo'), thread('c', 'Charlie')]

describe('ThreadList', () => {
  it('marks the active thread with aria-current and selects on click', () => {
    const onSelect = vi.fn()
    render(<ThreadList threads={T} activeId="b" onSelect={onSelect} />)
    const list = screen.getByRole('list', { name: 'Threads' })
    expect(within(list).getByText('Bravo').closest('button')?.getAttribute('aria-current')).toBe('true')
    expect(within(list).getByText('Alpha').closest('button')?.getAttribute('aria-current')).toBeNull()
    fireEvent.click(within(list).getByText('Charlie'))
    expect(onSelect).toHaveBeenCalledWith('c')
  })

  it('keeps the search field inside dash-search-adapter so the inner input is not a second box', () => {
    render(<ThreadList threads={T} activeId={null} onSelect={() => undefined} />)
    expect(screen.getByLabelText('Search conversations').closest('.dash-search-adapter')).toBeTruthy()
  })

  it('filters by title and preview, and says when nothing matches', async () => {
    render(<ThreadList threads={T} activeId={null} onSelect={() => undefined} />)
    const search = screen.getByLabelText('Search conversations')
    fireEvent.change(search, { target: { value: 'deploy' } })
    await act(async () => void (await vi.advanceTimersByTimeAsync(500)))
    expect(screen.queryByText('Bravo')).toBeNull()
    expect(screen.getByText('Alpha')).toBeTruthy()
    fireEvent.change(search, { target: { value: 'zzz' } })
    await act(async () => void (await vi.advanceTimersByTimeAsync(500)))
    expect(screen.getByText('No matches')).toBeTruthy()
  })

  it('moves between threads with the arrow keys and stops at the ends', () => {
    const onSelect = vi.fn()
    render(<ThreadList threads={T} activeId="b" onSelect={onSelect} />)
    const list = screen.getByRole('list', { name: 'Threads' })
    fireEvent.keyDown(list, { key: 'ArrowDown' })
    expect(onSelect).toHaveBeenLastCalledWith('c')
    fireEvent.keyDown(list, { key: 'ArrowUp' })
    expect(onSelect).toHaveBeenLastCalledWith('a')
  })

  it('shows an empty state and a retryable error', () => {
    const retry = vi.fn()
    const { rerender } = render(<ThreadList threads={[]} activeId={null} onSelect={() => undefined} />)
    expect(screen.getByText('No conversations')).toBeTruthy()
    rerender(<ThreadList threads={null} activeId={null} onSelect={() => undefined} error="Tether down" onRetry={retry} />)
    fireEvent.click(screen.getByText('Retry'))
    expect(retry).toHaveBeenCalled()
    expect(screen.getByRole('alert').textContent).toContain('Tether down')
  })
})

vi.mock('./thread-context', () => ({ useThreadContext: () => ({ ref: null, activity: [], files: [], work: [], state: 'ready' }) }))

describe('ChatView', () => {
  const view = (active: ChatThread | null = T[0]) => (
    <ChatView threads={T} activeThread={active} onSelectThread={() => undefined}><p>conversation body</p></ChatView>
  )

  it('starts with the details column open on a wide screen and shows honest empty context', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('1280'), addEventListener() {}, removeEventListener() {} }))
    try {
      render(view())
      const aside = screen.getByRole('complementary', { name: 'Thread details' })
      expect(within(aside).getByText('Reachability')).toBeTruthy()
      expect(within(aside).getByText('No activity in this thread yet.')).toBeTruthy()
      expect(within(aside).getByText('No files shared in this thread.')).toBeTruthy()
      expect(within(aside).getByText('No tasks or plan items linked to this thread.')).toBeTruthy()
      expect(screen.getByText('Details')).toBeTruthy()
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('on a narrow screen shows the list as home, then the thread with a back control', () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width: 1023'), addEventListener() {}, removeEventListener() {} }))
    const onBack = vi.fn()
    try {
      const { rerender } = render(<ChatView threads={T} activeThread={null} onSelectThread={() => undefined} onBack={onBack} />)
      expect(screen.getByRole('list', { name: 'Threads' })).toBeTruthy()
      expect(screen.queryByText('Select a conversation')).toBeNull()
      rerender(<ChatView threads={T} activeThread={T[0]} onSelectThread={() => undefined} onBack={onBack}><p>conversation body</p></ChatView>)
      fireEvent.click(screen.getByLabelText('Back to conversations'))
      expect(onBack).toHaveBeenCalledTimes(1)
      fireEvent.keyDown(document.body, { key: 'Escape' })
      expect(onBack).toHaveBeenCalledTimes(2)
      expect(screen.getByText('conversation body')).toBeTruthy()
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('starts with the details column collapsed and toggles it', () => {
    render(view())
    expect(screen.queryByRole('complementary', { name: 'Thread details' })).toBeNull()
    fireEvent.click(screen.getByLabelText('Show thread details'))
    const aside = screen.getByRole('complementary', { name: 'Thread details' })
    expect(within(aside).getAllByText('Alpha').length).toBeGreaterThan(0)
    fireEvent.click(screen.getByLabelText('Hide thread details'))
    expect(screen.queryByRole('complementary', { name: 'Thread details' })).toBeNull()
  })

  it('collapses the thread column', () => {
    render(view())
    expect(screen.getByRole('complementary', { name: 'Conversations' })).toBeTruthy()
    fireEvent.click(screen.getByLabelText('Hide conversations'))
    expect(screen.queryByRole('complementary', { name: 'Conversations' })).toBeNull()
  })

  it('asks for a selection when no thread is open', () => {
    render(view(null))
    expect(screen.getByText('Select a conversation')).toBeTruthy()
    expect(screen.queryByText('conversation body')).toBeNull()
  })
})

// jsdom has no layout, so this pins the classes that stop the page scrolling: the message
// action buttons hold position:absolute sr-only labels, and without a positioned, clipping
// ancestor they escape the message scroller and stretch the whole page (seen live, 2026-10-24).
describe('ChatView containment', () => {
  const clipped = (el: Element | null) => !!el && el.classList.contains('relative') && el.classList.contains('overflow-hidden')
  it('is a positioned, clipping container in both variants', () => {
    const { container, rerender } = render(<ChatView threads={T} activeThread={T[0]} onSelectThread={() => undefined}><p>x</p></ChatView>)
    expect(clipped(container.firstElementChild)).toBe(true)
    rerender(<ChatView variant="compact" threads={T} activeThread={T[0]} onSelectThread={() => undefined}><p>x</p></ChatView>)
    expect(clipped(container.firstElementChild)).toBe(true)
  })
})

describe('ChatView compact (the dock)', () => {
  it('shows the thread list, then the conversation with a way back, and no side columns', () => {
    const onSelect = vi.fn()
    const onBack = vi.fn()
    const { rerender } = render(<ChatView variant="compact" threads={T} activeThread={null} onSelectThread={onSelect} onBack={onBack}><p>conversation body</p></ChatView>)
    expect(screen.getByRole('list', { name: 'Threads' })).toBeTruthy()
    expect(screen.queryByLabelText('Back to threads')).toBeNull()
    expect(screen.queryByLabelText('Show thread details')).toBeNull()
    rerender(<ChatView variant="compact" threads={T} activeThread={T[0]} onSelectThread={onSelect} onBack={onBack}><p>conversation body</p></ChatView>)
    expect(screen.getByText('conversation body')).toBeTruthy()
    expect(screen.queryByRole('list', { name: 'Threads' })).toBeNull()
    fireEvent.click(screen.getByLabelText('Back to threads'))
    expect(onBack).toHaveBeenCalled()
  })
})

describe('ConversationView', () => {
  const base = { threadId: 't', messages: [], agentLabel: 'Alpha', loading: false, hasMore: false, onLoadOlder: () => undefined }

  it('sends the trimmed draft, clears it, and focuses the composer', async () => {
    const onSend = vi.fn().mockResolvedValue(undefined)
    render(<ConversationView {...base} onSend={onSend} />)
    const box = screen.getByLabelText('Message') as HTMLTextAreaElement
    expect(document.activeElement).toBe(box)
    fireEvent.change(box, { target: { value: '  hello  ' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await flush()
    expect(onSend).toHaveBeenCalledWith('hello')
    expect(box.value).toBe('')
  })

  it('keeps the draft and shows the error when the send fails', async () => {
    const onSend = vi.fn().mockRejectedValue(new Error('nope'))
    const { rerender } = render(<ConversationView {...base} onSend={onSend} />)
    const box = screen.getByLabelText('Message') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'draft' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await flush()
    expect(box.value).toBe('draft')
    rerender(<ConversationView {...base} onSend={onSend} sendError="nope" />)
    expect(screen.getByRole('alert').textContent).toBe('nope')
  })

  it('keeps an unsent draft per thread across switching away and back, and clears it after a send', async () => {
    const onSend = vi.fn().mockResolvedValue(undefined)
    const { unmount } = render(<ConversationView {...base} threadId="a" onSend={onSend} />)
    fireEvent.change(screen.getByLabelText('Message'), { target: { value: 'half written' } })
    unmount()
    cleanup()
    render(<ConversationView {...base} threadId="b" onSend={onSend} />)
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).value).toBe('')
    cleanup()
    render(<ConversationView {...base} threadId="a" onSend={onSend} />)
    const box = screen.getByLabelText('Message') as HTMLTextAreaElement
    expect(box.value).toBe('half written')
    fireEvent.keyDown(box, { key: 'Enter' })
    await flush()
    expect(onSend).toHaveBeenCalledWith('half written')
    cleanup()
    render(<ConversationView {...base} threadId="a" onSend={onSend} />)
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).value).toBe('')
  })

  it('disables the composer when the agent cannot receive', () => {
    render(<ConversationView {...base} onSend={async () => undefined} canSend={false} />)
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).disabled).toBe(true)
  })
})

describe('ChatPage', () => {
  function Where() { return <span data-testid="where">{useLocation().pathname}</span> }
  const setup = (path = '/chat') => {
    const client = createMockChatClient()
    render(
      <MemoryRouter initialEntries={[path]}>
        <AutolinkProvider>
          <ChatProvider client={client}>
            <Routes>
              <Route path="/chat" element={<ChatPage />} />
              <Route path="/chat/:threadId" element={<ChatPage />} />
            </Routes>
            <Where />
          </ChatProvider>
        </AutolinkProvider>
      </MemoryRouter>,
    )
    return client
  }

  it('lists real threads, opens one into the URL, and shows its conversation', async () => {
    setup()
    await flush()
    expect(screen.getByText('Select a conversation')).toBeTruthy()
    const list = screen.getByRole('list', { name: 'Threads' })
    fireEvent.click(within(list).getByText(MOCK_CHAT_AGENTS[1].agent.label))
    await flush()
    expect(screen.getByTestId('where').textContent).toMatch(/^\/chat\/.+/)
    expect(screen.getByLabelText('Message')).toBeTruthy()
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(MOCK_CHAT_AGENTS[1].agent.label)
  })

  it('sends through the client from the composer', async () => {
    const client = setup()
    await flush()
    fireEvent.click(within(screen.getByRole('list', { name: 'Threads' })).getByText(MOCK_CHAT_AGENTS[0].agent.label))
    await flush()
    const box = screen.getByLabelText('Message')
    fireEvent.change(box, { target: { value: 'status?' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await flush()
    expect(client.sentBodies()).toEqual(['status?'])
  })
})
