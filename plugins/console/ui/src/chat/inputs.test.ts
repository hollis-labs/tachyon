import { describe, expect, it } from 'vitest'
import { audienceOf, wakeOf } from './inputs'
import { toMessage } from './chatapi'
import mailWake from './fixtures/reply-audience/unknown_mail_wake.json'

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

  it("folds exactly Tether's generated mail-wake cases, in both wire shapes, and never an unproven binding", () => {
    const folded = Object.entries(replyAudience)
      .filter(([k]) => !k.endsWith('index.json'))
      .map(([k, f]) => {
        expect(wakeOf(f.event?.input_sources)).toBe(wakeOf(f.message_metadata?.input_sources)) // the event and the metadata agree
        return [k.replace(/^.*\//, '').replace('.json', ''), wakeOf(f.event?.input_sources)] as const
      })
    expect(folded.length).toBe(11)
    expect(folded.filter(([, w]) => w === 'mail').map(([k]) => k).sort()).toEqual(['agent', 'unknown_mail_wake', 'unknown_shared_operator_credential'])
    for (const unproven of ['unknown_no_input', 'unknown_queued_inputs_present', 'unknown_submission_unsettled', 'unknown_failed_input_ignored']) {
      expect(folded.find(([k]) => k === unproven)?.[1]).toBeUndefined()
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
