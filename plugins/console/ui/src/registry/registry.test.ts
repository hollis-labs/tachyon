import { describe, expect, it } from 'vitest'
import { type Project, profilePatch } from './api'

const p = {
  id: 'tachyon',
  identity: { display_name: 'Tachyon', description: 'd', tags: ['a'], status: 'active' },
  guidance: { entry_points: ['README.md'], guidelines: 'g' },
  link: { state: 'linked' },
} as Project

describe('profilePatch', () => {
  it('sends only changed fields', () => {
    expect(profilePatch(p, { display_name: 'Tachyon', tags: ['a', 'b'], status: 'active' })).toEqual({ tags: ['a', 'b'] })
  })
  it('is empty when nothing changed', () => {
    expect(profilePatch(p, { entry_points: ['README.md'] })).toEqual({})
  })
})
