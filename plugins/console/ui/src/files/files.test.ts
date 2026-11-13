import { describe, expect, it } from 'vitest'
import { applyCaps, denyMessage, errorMessage, FileError, formatSize } from './api'

describe('file viewer helpers', () => {
  it('formats sizes', () => {
    expect(formatSize(null)).toBe('')
    expect(formatSize(512)).toBe('512 B')
    expect(formatSize(2048)).toBe('2.0 KB')
    expect(formatSize(5 * 1024 * 1024)).toBe('5.0 MB')
  })
  it('treats hidden and missing the same, and never echoes a path', () => {
    expect(denyMessage('not_found_or_hidden')).toBe(denyMessage(undefined, false))
    expect(denyMessage('secret_pattern')).not.toContain('/')
  })
  it('turns 401/403 into an operator-access message, other errors into their own', () => {
    expect(errorMessage(new FileError(403, 'x', 'no'))).toBe('Operator access is needed to view files.')
    expect(errorMessage(new FileError(413, 'too_large', 'big'))).toBe('This file is too large to preview.')
    expect(errorMessage(new FileError(500, 'x', 'boom'))).toBe('boom')
  })
})

import { casterStat, contentUrl, parseCasterRef, pinCasterRef } from './api'

describe('preview caps', () => {
  const caps = { text_bytes: 2 * 1024 * 1024, image_bytes: 25 * 1024 * 1024, html_bytes: 2 * 1024 * 1024 }
  const stat = { path: '/x/a.json', exists: true, allowed: true, type: 'file' as const, kind: 'json' as const, size: 3 * 1024 * 1024 }
  it('reads an allowed text file over the text cap as too_large', () => {
    expect(applyCaps(stat, caps)).toMatchObject({ allowed: false, deny_reason: 'too_large', size: stat.size })
  })
  it('leaves files under the cap, images under the image cap, denied files and unknown caps alone', () => {
    expect(applyCaps({ ...stat, size: 1024 }, caps)).toEqual({ ...stat, size: 1024 })
    expect(applyCaps({ ...stat, kind: 'image' }, caps).allowed).toBe(true)
    expect(applyCaps({ ...stat, allowed: false, deny_reason: 'secret_pattern' }, caps).deny_reason).toBe('secret_pattern')
    expect(applyCaps(stat, null).allowed).toBe(true)
  })
})

describe('caster refs', () => {
  it('parses only the two allowed shapes', () => {
    expect(parseCasterRef('caster://designer/page@3')).toEqual({ ns: 'designer', name: 'page', rev: 3 })
    expect(parseCasterRef('caster://designer/page')).toEqual({ ns: 'designer', name: 'page', rev: null })
    expect(parseCasterRef('caster://designer/page@0')).toBeNull()
    expect(parseCasterRef('caster://designer/a/b')).toBeNull()
  })
  it('pins a revision and builds the caster content url', () => {
    expect(pinCasterRef('caster://designer/page@2', 5)).toBe('caster://designer/page@5')
    expect(contentUrl('caster://designer/page@5')).toContain('caster_ref=caster%3A%2F%2Fdesigner%2Fpage%405')
    expect(contentUrl('/tmp/a.txt')).toContain('path=')
    expect(casterStat({ ref: 'caster://d/p', namespace: 'd', name: 'p', rev: 1, latest_rev: 1, floating: false, revisions: [] }, 'caster://d/p@1').kind).toBe('binary')
  })
})
