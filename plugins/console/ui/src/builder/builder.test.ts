import { describe, expect, it } from 'vitest'
import { parseEvent } from './stream'
import { type AgentDetail, BuilderError, errorMessage } from './api'
import { buildOps, buildPartOps, changedPartPaths, changingText, changedPaths, isOpen, minutesAndSeconds, relativeTime } from './changes'
import { handoffStatus } from './HandoffPanel'
import { SECTIONS, STATE_LABEL } from './vocab'

describe('builder helpers', () => {
  it('names every state and keeps sections in reading order', () => {
    expect(Object.keys(STATE_LABEL)).toEqual(['active', 'disabled', 'retired'])
    expect(SECTIONS.map((s) => s.id)).toEqual(['identity', 'runtime', 'prompt', 'tools', 'policy', 'used_by', 'history'])
  })
  it('tolerates operator-only reads with a plain message', () => {
    expect(errorMessage(new BuilderError(403, 'x', 'no'))).toBe('Operator access is needed to view this.')
    expect(errorMessage(new BuilderError(500, 'x', 'boom'))).toBe('boom')
  })
})

describe('hand-off status', () => {
  const base = { change_id: 'c1', request_id: 'r1' }
  it('says what the operator is waiting on, never claims immediate activation', () => {
    expect(handoffStatus({ ...base, state: 'awaiting_handoff' }).text).toBe('Waiting for you to plan, approve and apply in Tether.')
    expect(handoffStatus({ ...base, state: 'awaiting_approval', plan: { plan_id: 'plan_ABC12345', state: 'planned', digest: '', expires_at: 0, approve_command: '' } }).text).toBe('Plan plan_ABC is waiting for approval in Tether.')
    const applied = handoffStatus({ ...base, state: 'applied', handoff: { mode: 'copy', name: 'n', bundle_sha256: '', files: 2, session_available: false } })
    expect(applied.text).toBe('Applied. Detected 2 files changed.')
    expect(applied.lines.join(' ')).toContain('new launches use the update')
    expect(applied.lines.join(' ')).not.toMatch(/immediate/i)
    expect(handoffStatus({ ...base, state: 'stale' }).text).toBe('Changed elsewhere. Review before preparing again.')
    expect(handoffStatus({ ...base, state: 'failed', failure: { step: 'validate', code: 'x', message: 'bad' } })).toMatchObject({ text: 'Failed: validate: bad.', danger: true })
  })
  it('keeps awaiting_handoff open and polled', () => {
    expect(isOpen('awaiting_handoff')).toBe(true)
  })
})

const agent = {
  id: 'a1',
  identity: { name: 'A', description: 'd', roles: ['x'] },
  runtime: { model: 'm', enabled: true },
  revision: { sha: 'abc' },
} as unknown as AgentDetail

describe('changes', () => {
  it('ignores unchanged draft fields', () => {
    expect(changedPaths(agent, { 'runtime.model': 'm', 'identity.roles': ['x'] })).toEqual([])
  })
  it('builds one set op per changed field carrying the sha', () => {
    const ops = buildOps(agent, { 'runtime.model': 'n', 'identity.roles': ['x'] })
    expect(ops).toEqual([
      {
        target: { kind: 'agent', id: 'a1' },
        expected_sha: 'abc',
        op: { type: 'set', path: 'runtime.model', value: 'n' },
      },
    ])
  })
  it('knows open states and formats the countdown', () => {
    expect(isOpen('awaiting_approval')).toBe(true)
    expect(isOpen('applied')).toBe(false)
    expect(minutesAndSeconds(125000)).toBe('2:05')
    const now = 1_800_000_000_000
    expect(relativeTime(now - 12 * 60000, now)).toBe('12m ago')
    expect(relativeTime((now - 30 * 3600000) / 1000, now)).toBe('Yesterday')
  })
})

describe('part ops', () => {
  const policy = { id: 'strict', revision: 4, name: 'Strict', summary: '', bundle: { network: 'off', mcp_scopes: ['a'] } }
  it('reads policy fields from the bundle and ignores unchanged ones', () => {
    expect(changedPartPaths('policies', policy, { network: 'off', mcp_scopes: ['a'] })).toEqual([])
  })
  it('targets the part with its revision as expected_sha', () => {
    expect(buildPartOps('policies', policy, { network: 'on' })).toEqual([
      { target: { kind: 'part', id: 'strict', part_kind: 'policy' }, expected_sha: '4', op: { type: 'set', path: 'network', value: 'on' } },
    ])
  })
})

describe('changingText', () => {
  it('lists what changed', () => {
    expect(changingText([])).toBe('No unsaved changes')
    expect(changingText(['Body'])).toBe('1 unsaved change · Body')
    expect(changingText(['Body', 'Summary'])).toBe('2 unsaved changes · Body, Summary')
  })
})

describe('parseEvent', () => {
  it('accepts known flat events and drops the rest', () => {
    expect(parseEvent('{"type":"change","at":1,"change_id":"c","state":"approved"}')).toMatchObject({ type: 'change', change_id: 'c' })
    expect(parseEvent('{"type":"nope","at":1}')).toBeNull()
    expect(parseEvent('not json')).toBeNull()
  })
})
