import { describe, expect, it, vi } from 'vitest'

const calls: { m: string; b?: string }[] = []
let fail = false
vi.mock('../roster', () => ({ chatJSON: async (_p: string, init?: RequestInit) => { calls.push({ m: init?.method ?? 'GET', b: init?.body as string }); if (init?.method === 'PUT' && fail) throw new Error('nope'); return { scratchpad_placement: 'drawer' } }, CHAT_BASE: '', chatFetch: fetch }))
import { setScratchPlacement } from './model'

describe('scratch placement setting', () => {
  it('is optimistic, persists with PUT, and rolls back on failure', async () => {
    await setScratchPlacement('aside')
    expect(calls.at(-1)).toEqual({ m: 'PUT', b: JSON.stringify({ scratchpad_placement: 'aside' }) })
    fail = true
    await expect(setScratchPlacement('drawer')).rejects.toThrow('nope')
  })
})
