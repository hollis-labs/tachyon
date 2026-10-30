import { describe, expect, it } from 'vitest'
import { lensOfSlug, lensPath } from './vocabulary'

describe('lens routes', () => {
  it('names the streams lens by its slug and accepts the database name as an alias', () => {
    expect(lensPath('workstreams')).toBe('/planner/streams')
    expect(lensPath('workstreams', 'a b')).toBe('/planner/streams/a%20b')
    expect(lensOfSlug('streams')).toBe('workstreams')
    expect(lensOfSlug('workstreams')).toBe('workstreams')
    expect(lensOfSlug('nonsense')).toBeUndefined()
  })
})
