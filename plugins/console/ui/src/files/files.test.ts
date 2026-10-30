import { describe, expect, it } from 'vitest'
import { denyMessage, errorMessage, FileError, formatSize } from './api'

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
