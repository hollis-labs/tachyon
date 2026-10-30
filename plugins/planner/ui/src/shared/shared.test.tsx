import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventTimeline, OptionCompare, SegmentedProgress, SpineList, TriageStack } from '@planner/shared'

afterEach(cleanup)

describe('SegmentedProgress', () => {
  it('exposes the text equivalent and counts done', () => {
    render(<SegmentedProgress label="3 of 8 done, 1 blocked" items={[{ state: 'done', count: 3 }, { state: 'blocked', count: 1 }, { state: 'todo', count: 4 }]} />)
    const bar = screen.getByRole('progressbar', { name: '3 of 8 done, 1 blocked' })
    expect(bar.getAttribute('aria-valuenow')).toBe('3')
    expect(bar.getAttribute('aria-valuemax')).toBe('8')
  })
})

describe('SpineList', () => {
  it('moves focus with J/K and opens with Enter', () => {
    const onSelect = vi.fn()
    render(<SpineList label="Items" onSelect={onSelect} selectedId="a" groups={[{ id: 'g', title: 'Group', items: [{ id: 'a', title: 'A' }, { id: 'b', title: 'B' }] }]} />)
    const [a, b] = screen.getAllByRole('option')
    a.focus()
    fireEvent.keyDown(a, { key: 'j' })
    expect(document.activeElement).toBe(b)
    fireEvent.keyDown(b, { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('b')
  })
})

describe('EventTimeline', () => {
  const events = [
    { id: '1', day: 'Today', time: '10:00', facet: 'tasks', body: 'Task done' },
    { id: '2', day: 'Today', time: '09:00', facet: 'blockers', tone: 'blocked' as const, body: 'Blocked' },
  ]
  const facets = [{ id: 'all', label: 'All', count: 2 }, { id: 'tasks', label: 'Tasks', count: 1 }]
  it('filters by facet and says so when nothing matches', () => {
    render(<EventTimeline events={events} facets={facets} allFacet="all" facetsLabel="Filter" streamLabel="Events" emptyLabel="Nothing yet" filteredEmptyLabel="Nothing matches" />)
    fireEvent.click(screen.getByRole('button', { name: /Tasks/ }))
    expect(screen.queryByText('Blocked')).toBeNull()
    expect(screen.getByText('Task done')).toBeTruthy()
  })
})

describe('TriageStack', () => {
  it('routes with its key and says so when everything is triaged', () => {
    const onRoute = vi.fn()
    const labels = { count: (n: number) => `${n} left`, allTriaged: 'All triaged', routeHeading: 'Into', dismiss: 'Dismiss', next: (t: string) => t }
    const { container, rerender } = render(<TriageStack card={<b>Card</b>} remaining={1} routes={[{ id: 'a', label: 'A', key: 'a' }]} onRoute={onRoute} onDismiss={vi.fn()} labels={labels} />)
    fireEvent.keyDown(container.firstElementChild!, { key: 'a' })
    expect(onRoute).toHaveBeenCalledWith('a')
    rerender(<TriageStack card={null} remaining={0} routes={[]} onRoute={onRoute} onDismiss={vi.fn()} labels={labels} />)
    expect(screen.getByText('All triaged')).toBeTruthy()
  })
})

describe('OptionCompare', () => {
  it('dims the others once chosen and hides Choose', () => {
    const labels = { choose: (l: string) => `Choose ${l}`, chooseButton: 'Choose', recommended: 'Rec', chosen: 'Chosen', pros: 'Pros', cons: 'Cons', groupLabel: 'Options' }
    render(<OptionCompare options={[{ id: 'a', label: 'A' }, { id: 'b', label: 'B' }]} chosenId="a" onChoose={vi.fn()} labels={labels} />)
    expect(screen.queryByRole('button', { name: /Choose/ })).toBeNull()
    expect(screen.getByText('Chosen')).toBeTruthy()
  })
})
