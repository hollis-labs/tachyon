import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { answerCall, AskCardView, type AskCard } from './card'
import { NeedsYouPage, type NeedsYouResult } from './page'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const decision: AskCard = {
  kind: 'decision', subject: 'Keep or patch the dialog?', body: 'FormDialog ignores **Escape**.', reply_to: '@lead', default_option: 'keep', default_at: '2026-11-15T17:00Z',
  options: [{ id: 'keep', label: 'Keep it', recommended: true, detail: 'the reviewer agrees' }, { id: 'patch', label: 'Patch the kit', detail: 'a kit release' }],
}

describe('typed ask cards (read-only)', () => {
  it('shows the ask: kind, subject, body, options with the recommended one and why, and the default', () => {
    render(<AskCardView card={decision} messageId="m1" />)
    expect(screen.getByText('Decision')).toBeTruthy()
    expect(screen.getByText('Keep or patch the dialog?')).toBeTruthy()
    expect(screen.getByText('Escape').tagName).toBe('STRONG')
    const opts = within(screen.getByRole('list', { name: 'Options' })).getAllByRole('listitem')
    expect(opts.map((o) => o.getAttribute('data-option'))).toEqual(['keep', 'patch'])
    expect(opts[0].textContent).toContain('Recommended')
    expect(opts[0].textContent).toContain('the reviewer agrees')
    expect(opts[1].textContent).not.toContain('Recommended')
    expect(screen.getByText('If nobody answers: Keep it (at 2026-11-15T17:00Z)')).toBeTruthy()
    expect(screen.queryByRole('button', { name: /approve|answer/i })).toBeNull() // read-only: no answer buttons
  })

  it('gives one copyable answer line per option: a reply to the ask with answer.option', () => {
    const line = answerCall(decision, 'm1', decision.options![1])
    expect(line.startsWith('tether_message_post ')).toBe(true)
    expect(JSON.parse(line.slice('tether_message_post '.length))).toEqual({ kind: 'message', to: ['@lead'], in_reply_to: 'm1', answer: { option: 'patch' }, body: 'Patch the kit' })
    render(<AskCardView card={decision} messageId="m1" />)
    expect(screen.getAllByRole('button', { name: 'Copy' })).toHaveLength(2)
    expect(screen.getByText(/Answering from Tachyon is not available yet/)).toBeTruthy()
  })

  it('opens from a needs-you row', async () => {
    const body: NeedsYouResult = { items: [{ id: 'ask:m1', source: 'ask', kind: 'decision', why: 'reply', title: 'Keep or patch the dialog?', from: '@lead', since: new Date().toISOString(), critical: false, message_id: 'm1', card: decision }],
      sources: { asks: { ok: true }, mail: { ok: true }, statuses: { ok: true } }, checked_at: new Date().toISOString() }
    render(<MemoryRouter><NeedsYouPage fetch={vi.fn((_url: string) => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })))} /></MemoryRouter>)
    const toggle = await screen.findByRole('button', { name: 'Show the ask' })
    expect(document.querySelector('[data-card]')).toBeNull()
    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(document.querySelector('[data-card="decision"]')).toBeTruthy()
  })
})
