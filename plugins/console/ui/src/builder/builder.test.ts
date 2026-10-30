import { describe, expect, it } from 'vitest'
import { BuilderError, errorMessage } from './api'
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
