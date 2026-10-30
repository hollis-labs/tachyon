import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConversationView } from '../view/ConversationView'
import { AsideProvider } from '../aside/context'
import { useChatKeys } from './keys'
import type { ChatMessage } from '../types'

afterEach(cleanup)
const msg = (id: string, from: 'agent' | 'operator'): ChatMessage => ({ id, threadId: 't', from, body: id, at: Date.now(), tools: [] })

function Keys({ pick, toggle }: { pick: (p: string) => void; toggle: (r: string) => void }) {
  useChatKeys({ pick, panel: 'plan', open: true, toggleBookmark: toggle })
  return (
    <main aria-label="Conversation">
      <article id="m1" data-from="agent" tabIndex={-1}>one</article>
      <article id="m2" data-from="agent" tabIndex={-1}>two</article>
      <input aria-label="field" />
    </main>
  )
}

describe('chat keys', () => {
  it('G then a letter opens a panel; typing in a field is ignored', () => {
    const pick = vi.fn()
    render(<Keys pick={pick} toggle={vi.fn()} />)
    fireEvent.keyDown(window, { key: 'g' })
    fireEvent.keyDown(window, { key: 'a' })
    expect(pick).toHaveBeenCalledWith('approvals')
    const field = screen.getByLabelText('field')
    field.focus()
    fireEvent.keyDown(field, { key: 'g', bubbles: true })
    fireEvent.keyDown(field, { key: 'p', bubbles: true })
    expect(pick).toHaveBeenCalledTimes(1)
  })
  it('J/K move between messages and B bookmarks the focused one', () => {
    const toggle = vi.fn()
    render(<Keys pick={vi.fn()} toggle={toggle} />)
    fireEvent.keyDown(window, { key: 'j' })
    expect(document.activeElement?.id).toBe('m1')
    fireEvent.keyDown(document.activeElement!, { key: 'j', bubbles: true })
    expect(document.activeElement?.id).toBe('m2')
    fireEvent.keyDown(document.activeElement!, { key: 'k', bubbles: true })
    expect(document.activeElement?.id).toBe('m1')
    fireEvent.keyDown(document.activeElement!, { key: 'b', bubbles: true })
    expect(toggle).toHaveBeenCalledWith('m1')
  })
})

describe('conversation states', () => {
  const base = { threadId: 't', messages: [] as ChatMessage[], agentLabel: 'Alpha', loading: false, hasMore: false, onLoadOlder: () => undefined, onSend: async () => undefined }
  it('a new thread shows who the agent is and suggestions that fill the composer', () => {
    render(<AsideProvider wide><ConversationView {...base} role="portfolio-manager" /></AsideProvider>)
    expect(screen.getByText('portfolio-manager')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Draft a plan for what is next.' }))
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).value).toBe('Draft a plan for what is next.')
  })
  it('a stream failure shows Retry above the composer and keeps the composer usable', () => {
    const onRetry = vi.fn()
    render(<AsideProvider wide><ConversationView {...base} messages={[msg('m1', 'agent')]} error="Lost the connection." onRetry={onRetry} /></AsideProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
    expect((screen.getByLabelText('Message') as HTMLTextAreaElement).disabled).toBe(false)
  })
  it('@ offers agents and Tab inserts the first match', () => {
    render(<AsideProvider wide><ConversationView {...base} mentions={['portfolio-manager', 'task-ops']} /></AsideProvider>)
    const box = screen.getByLabelText('Message') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'hey @po' } })
    expect(screen.getByRole('option', { name: '@portfolio-manager' })).toBeTruthy()
    fireEvent.keyDown(box, { key: 'Tab' })
    expect(box.value).toBe('hey @portfolio-manager ')
  })
})
