import { describe, expect, it } from 'vitest'
import { type AgentDetail, BuilderError, errorMessage } from './api'
import { buildOps, buildPartOps, changedPartPaths, changedPaths, isOpen, minutesAndSeconds, relativeTime } from './changes'
import { SECTIONS, STATE_LABEL } from './vocab'

describe('builder helpers', () => {
  it('names every state and keeps sections in reading order', () => {
    expect(Object.keys(STATE_LABEL)).toEqual(['active', 'disabled', 'retired'])
    expect(SECTIONS.map((s) => s.id)).toEqual(['identity', 'runtime', 'prompt', 'tools', 'policy', 'resolved', 'used_by', 'checks', 'history'])
  })
  it('tolerates operator-only reads with a plain message', () => {
    expect(errorMessage(new BuilderError(403, 'x', 'no'))).toBe('Operator access is needed to view this.')
    expect(errorMessage(new BuilderError(500, 'x', 'boom'))).toBe('boom')
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
