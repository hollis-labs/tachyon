import { describe, expect, it } from 'vitest'
import { type AgentDetail, BuilderError, errorMessage } from './api'
import { buildOps, changedPaths, isOpen, minutesAndSeconds } from './changes'
import { SECTIONS, STATE_LABEL } from './vocab'

describe('builder helpers', () => {
  it('names every state and keeps sections in reading order', () => {
    expect(Object.keys(STATE_LABEL)).toEqual(['active', 'disabled', 'retired'])
    expect(SECTIONS.map((s) => s.id)).toEqual(['identity', 'runtime', 'prompt', 'tools', 'policy'])
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
  })
})
