import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { applyStreamEvent } from './data'
import { ContextMeter, OneLiner, ageText, stateText } from './session-parts'
import { SessionCards, matchesFilter, matchesQuery } from './sessions-view'
import type { Session, SessionsResponse } from './types'

afterEach(cleanup)

const session = (over: Partial<Session> & { id: string }): Session => ({
  agent: { urn: null, name: 'builder', logical_agent_id: null }, kind: 'chat', provider: 'claude', model: null, workroot: '/w/tachyon', state: 'working', stale_reason: null, stale_since: null,
  needs_you: { reason: null, since: null }, started_at: 0, last_activity_at: null, runtime_ms: 0, one_liner: null,
  context: { used: null, window: null, pct: null, source: 'unknown', breakdown: null }, tokens: { metered: false, input: null, output: null, cache_read: null, cache_write: null },
  tool_call_count: null, last_clear_at: null, pending: { approval: false, question: false, known: false }, assignment: null, spark: [], capabilities: { nudge: true, interrupt: { available: true, reason: null }, restart: true }, ...over,
})
const data = (sessions: Session[]): SessionsResponse => ({ sessions, summary: { working: sessions.length }, collector: { collecting: true, sessions: sessions.length, last_event_at: null, strategy: 'snapshot' } })

describe('applyStreamEvent', () => {
  it('merges a session snapshot into the known session and adds an unknown one', () => {
    const d = data([session({ id: 'a' })])
    const merged = applyStreamEvent(d, { type: 'session', at: 1, session: { id: 'a', state: 'idle' } })
    expect(merged.sessions[0].state).toBe('idle')
    expect(merged.sessions[0].agent.name).toBe('builder')
    expect(applyStreamEvent(merged, { type: 'session', at: 2, session: session({ id: 'b' }) }).sessions).toHaveLength(2)
  })
  it('ignores bus, control and gap here (a gap triggers a refetch in the hook)', () => {
    const d = data([session({ id: 'a' })])
    expect(applyStreamEvent(d, { type: 'bus', at: 1 })).toBe(d)
    expect(applyStreamEvent(d, { type: 'gap', at: 1 })).toBe(d)
  })
})

describe('session parts', () => {
  it('says unknown context as words and draws no bar, never a zero', () => {
    render(<ContextMeter pct={null} />)
    expect(screen.getByText('context unknown')).toBeTruthy()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })
  it('exposes context use as a progressbar value, danger from 90', () => {
    const { container } = render(<ContextMeter pct={92} />)
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('92')
    expect(container.querySelector('.bg-danger')).not.toBeNull()
  })
  it('formats ages by the largest whole unit', () => {
    expect([5000, 180000, 7200000, 432000000].map(ageText)).toEqual(['5s', '3m', '2h', '5d'])
  })
  it('renders verb, target and age, or the fallback', () => {
    const { rerender } = render(<OneLiner line={{ verb: 'editing', target: 'a.go', tool: 'Edit', at: 0, calls_this_turn: 1 }} now={60000} fallback="none" />)
    expect(screen.getByText('a.go')).toBeTruthy()
    rerender(<OneLiner line={null} now={0} fallback="no activity yet" />)
    expect(screen.getByText('no activity yet')).toBeTruthy()
  })
  it('words the state: needs you wins, stale says why, idle says how long', () => {
    expect(stateText(session({ id: 'a', needs_you: { reason: 'approval', since: 1 } }), 0)).toBe('Needs approval')
    expect(stateText(session({ id: 'a', state: 'stale', stale_reason: 'worker_offline' }), 0)).toBe('Stale: Worker offline')
    expect(stateText(session({ id: 'a', state: 'idle', last_activity_at: 0 }), 432000000)).toBe('idle 5d')
  })
})

describe('filters', () => {
  it('Needs you keeps a stale session that is also waiting', () => {
    const s = session({ id: 'a', state: 'stale', stale_reason: 'no_events', needs_you: { reason: 'question', since: 1 } })
    expect(matchesFilter(s, 'needs_you')).toBe(true)
    expect(matchesFilter(s, 'stale')).toBe(true)
    expect(matchesFilter(s, 'working')).toBe(false)
  })
  it('searches name, workroot, tool and task', () => {
    const s = session({ id: 'a', one_liner: { verb: 'running', target: 'go test', tool: 'Bash', at: 0, calls_this_turn: 1 }, assignment: { task_ref: 'CW-1' } })
    expect(['builder', 'tachyon', 'bash', 'cw-1', 'nope'].map((q) => matchesQuery(s, q))).toEqual([true, true, true, true, false])
  })
})

describe('session body', () => {
  it('omits what is unknown instead of printing placeholders', () => {
    const { rerender } = render(<SessionCards sessions={[session({ id: 'a' })]} now={0} onSelect={() => undefined} />)
    for (const t of ['no activity yet', 'context unknown']) expect(screen.queryByText(t)).toBeNull()
    expect(screen.queryByText(/^up /)).toBeNull()
    rerender(<SessionCards sessions={[session({ id: 'a', runtime_ms: 120000, context: { used: 1, window: 2, pct: 50, source: 'stream', breakdown: null } })]} now={0} onSelect={() => undefined} />)
    expect(screen.getByRole('progressbar')).toBeTruthy()
  })
})
