import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { lensGroups } from './lens-page'
import type { PlannerItem } from '@planner/api/types'

const stream = (id: string, counts?: { done: number; total: number }, progress?: { done: number; total: number }) => ({ id, title: id, status: 'active', rev: 1, counts: counts ?? { done: null, total: null }, ...(progress ? { progress } : {}) }) as unknown as PlannerItem

describe('Streams spine progress', () => {
  it('draws a bar for streams with counts (or the older progress), none for an unlinked stream', () => {
    const [group] = lensGroups('workstreams', [stream('a', { done: 3, total: 8 }), stream('b'), stream('c', undefined, { done: 5, total: 10 })], () => false, 0)
    render(<>{group.items.map((i) => <div key={i.id}>{i.progress}</div>)}</>)
    const bar = screen.getAllByRole('progressbar')[0]
    expect(bar.getAttribute('aria-valuenow')).toBe('3')
    expect(bar.getAttribute('aria-valuemax')).toBe('8')
    expect(screen.getAllByRole('progressbar')).toHaveLength(2)
  })
})
