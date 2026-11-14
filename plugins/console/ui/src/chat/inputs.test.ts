import { describe, expect, it } from 'vitest'
import { audienceOf, wakeOf } from './inputs'
import { toMessage } from './chatapi'
import mailWake from './fixtures/input-sources/mail_wake.json'

const replyAudience = import.meta.glob('./fixtures/reply-audience/*.json', { eager: true, import: 'default' }) as Record<string, { event?: { input_sources?: string[] }; message_metadata?: { input_sources?: string } }>

describe('input attribution', () => {
  it('reads the audience from proven input classes only', () => {
    expect(audienceOf('operator')).toBe('operator')
    expect(audienceOf(['agent'])).toBe('agent')
    expect(audienceOf('system')).toBe('system')
    expect(audienceOf('agent,system')).toBe('unknown')
    expect(audienceOf('')).toBe('unknown')
    expect(audienceOf(undefined)).toBe('unknown')
  })

  it('folds only a turn Tether proved was a mail wake, in either wire shape', () => {
    expect(wakeOf(mailWake.event.input_sources)).toBe('mail')
    expect(wakeOf(mailWake.message_metadata.input_sources)).toBe('mail')
    expect(wakeOf(' mail_wake ,mail_wake')).toBe('mail')
    for (const other of ['chat_send', 'session_turn', 'mcp_send_turn', 'routing_reply', 'system', 'mail_wake,chat_send', '', undefined]) expect(wakeOf(other)).toBeUndefined()
  })

  it('never folds an output without input_sources: every reply-audience fixture, queued and unknown ones included', () => {
    const cases = Object.entries(replyAudience).filter(([k]) => !k.endsWith('index.json'))
    expect(cases.length).toBeGreaterThan(5)
    for (const [, f] of cases) {
      expect(wakeOf(f.event?.input_sources)).toBeUndefined()
      expect(wakeOf(f.message_metadata?.input_sources)).toBeUndefined()
    }
  })

  it('maps the chat API meta onto the message, for Tether turns only', () => {
    const wake = toMessage('chatapi:tether:s1', { seq: 1, id: 'w', role: 'agent', text: 'Read 1 message.', at: 1, meta: { input_sources: ['mail_wake'] } })
    expect(wake.wake).toBe('mail')
    expect(wake.audience).toBeUndefined() // no proven class: the filter treats it as unknown
    const asked = toMessage('chatapi:tether:s1', { seq: 2, id: 'a', role: 'agent', text: 'Done.', at: 2, meta: { input_classes: ['agent'], input_sources: ['routing_reply'] } })
    expect([asked.wake, asked.audience]).toEqual([undefined, 'agent'])
    const mine = toMessage('chatapi:tether:s1', { seq: 3, id: 'o', role: 'operator', text: 'hi', at: 3, meta: { input_sources: ['mail_wake'] } })
    expect(mine.wake).toBeUndefined()
  })
})
