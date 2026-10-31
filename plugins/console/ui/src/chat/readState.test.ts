import { beforeEach, describe, expect, it, vi } from 'vitest'

const calls: string[] = []
let status = 403
vi.mock('../lib/http', () => ({ consoleJson: async (p: string) => { calls.push(p); if (status >= 400) throw Object.assign(new Error('x'), { status }); return {} }, CONSOLE_API_BASE: '' }))
import { markReadQuiet, resetReadStateForTest } from './readState'

beforeEach(() => { calls.length = 0; resetReadStateForTest(); status = 403 })

describe('mark read without operator access', () => {
  it('asks once, then stops asking until the block lapses', async () => {
    await markReadQuiet('a'); await markReadQuiet('b'); await markReadQuiet('c')
    expect(calls).toEqual(['/chat/threads/a/read'])
  })
  it('keeps marking when allowed', async () => {
    status = 200
    await markReadQuiet('a'); await markReadQuiet('b')
    expect(calls).toHaveLength(2)
  })
})
