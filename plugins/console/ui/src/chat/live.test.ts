import { describe, expect, it, vi } from 'vitest'
import { __test, type Frame } from './live'

class FakeES { onopen: (() => void) | null = null; onerror: (() => void) | null = null; onmessage: ((m: { data: string }) => void) | null = null; closed = false; close() { this.closed = true } }

describe('shared event hub', () => {
  it('opens one stream for many subscribers, closes with the last, and signals a reconnect', () => {
    vi.stubGlobal('EventSource', FakeES)
    const made: FakeES[] = []
    const h = __test.hub('/x', () => { const e = new FakeES(); made.push(e); return e as unknown as EventSource })
    const a: Frame[] = [], b: Frame[] = []
    const offA = h.subscribe((f) => a.push(f))
    const offB = h.subscribe((f) => b.push(f))
    expect(made).toHaveLength(1)
    made[0].onmessage?.({ data: JSON.stringify({ type: 'card', thread_ref: 't' }) })
    made[0].onmessage?.({ data: 'not json' })
    made[0].onerror?.(); made[0].onopen?.()
    expect(a.map((f) => f.type)).toEqual(['card', 'dropped', 'reconnected'])
    expect(b).toHaveLength(3)
    offA(); expect(made[0].closed).toBe(false)
    offB(); expect(made[0].closed).toBe(true)
    vi.unstubAllGlobals()
  })
})
