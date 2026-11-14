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

  it('gives one copyable answer line per option, built from ids only and single-quoted', () => {
    const line = answerCall(decision, 'm1', decision.options![1])!
    expect(line).toBe(`tether_message_post '{"kind":"message","to":["@lead"],"in_reply_to":"m1","answer":{"option":"patch"},"body":"Answer: patch"}'`)
    expect(line).not.toContain('Patch the kit') // the label is sender text: never copied
    render(<AskCardView card={decision} messageId="m1" />)
    expect(screen.getAllByRole('button', { name: 'Copy' })).toHaveLength(2)
    expect(screen.getByText(/Answering from Tachyon is not available yet/)).toBeTruthy()
  })

  const HOSTILE = ['$(touch /tmp/pwned)', '`id`', '"', "'", ';', '|', 'a\nb']

  it('never copies sender text: a hostile label is left out, a hostile id gives no line', () => {
    for (const bad of HOSTILE) {
      const label = `Keep ${bad} it`
      const fromLabel = answerCall(decision, 'm1', { id: 'keep', label } as { id: string })!
      expect(fromLabel).toBe(`tether_message_post '{"kind":"message","to":["@lead"],"in_reply_to":"m1","answer":{"option":"keep"},"body":"Answer: keep"}'`)
      expect(answerCall(decision, 'm1', { id: `keep${bad}` })).toBeNull()
      expect(answerCall(decision, `m1${bad}`, { id: 'keep' })).toBeNull()
      expect(answerCall({ ...decision, reply_to: `@lead${bad}` }, 'm1', { id: 'keep' })).toBeNull()
    }
    expect(answerCall({ ...decision, reply_to: undefined }, 'm1', { id: 'keep' })).toBeNull()
    // The card shows no copy line for a hostile id, and says why.
    render(<AskCardView card={{ ...decision, options: [{ id: 'x$(id)', label: 'Bad' }, { id: 'keep', label: 'Keep $(id)' }] }} messageId="m1" />)
    expect(screen.getAllByRole('button', { name: 'Copy' })).toHaveLength(1)
    expect(screen.getByText(/Bad: No answer line/)).toBeTruthy()
    for (const code of Array.from(document.querySelectorAll('code'))) {
      expect(code.textContent).not.toMatch(/[$`|;\n]/)
      expect((code.textContent ?? '').split("'").length).toBe(3) // exactly the two wrapping quotes
    }
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
