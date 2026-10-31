import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { lensGroups } from './lens-page'
import type { PlannerItem } from '@planner/api/types'

const stream = (id: string, counts?: { done: number; total: number }) => ({ id, title: id, status: 'active', rev: 1, ...(counts ? { counts } : {}) }) as unknown as PlannerItem

describe('Streams spine progress', () => {
  it('draws a bar only for streams the list sent counts for', () => {
    const [group] = lensGroups('workstreams', [stream('a', { done: 3, total: 8 }), stream('b')], () => false, 0)
    render(<>{group.items.map((i) => <div key={i.id}>{i.progress}</div>)}</>)
    const bar = screen.getByRole('progressbar')
    expect(bar.getAttribute('aria-valuenow')).toBe('3')
    expect(bar.getAttribute('aria-valuemax')).toBe('8')
    expect(screen.getAllByRole('progressbar')).toHaveLength(1)
  })
})
