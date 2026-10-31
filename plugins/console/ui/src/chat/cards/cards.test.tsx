import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { ApprovalCard, DecisionCard, PlanCard } from './Cards'
import { RunCard } from './RunCard'
import { demoBlocks } from './fixtures'
import { UNDO_MS } from './useAnswer'
import type { StreamBlock } from './types'

afterEach(() => { cleanup(); vi.useRealTimers() })
const get = <K extends StreamBlock['kind']>(k: K) => demoBlocks(0).find((b) => b.kind === k) as Extract<StreamBlock, { kind: K }>

describe('answerable cards', () => {
  it('collapses a reversible decision to a result line with Undo, and commits only after the window', async () => {
    vi.useFakeTimers()
    const commit = vi.fn().mockResolvedValue(undefined)
    render(<DecisionCard block={get('decision')} commit={commit} />)
    fireEvent.click(screen.getByRole('button', { name: 'Choose Hybrid' }))
    expect(screen.getByRole('status').textContent).toContain('You chose')
    expect(commit).not.toHaveBeenCalled()
    await act(async () => { vi.advanceTimersByTime(UNDO_MS + 500) })
    expect(commit).toHaveBeenCalledTimes(1)
  })

  it('Undo returns the card to answerable and never commits', async () => {
    vi.useFakeTimers()
    const commit = vi.fn().mockResolvedValue(undefined)
    render(<DecisionCard block={get('decision')} commit={commit} />)
    fireEvent.click(screen.getByRole('button', { name: 'Choose A2UI only' }))
    fireEvent.click(screen.getByRole('button', { name: /Undo/ }))
    expect(screen.getByRole('button', { name: 'Choose Hybrid' })).toBeTruthy()
    await act(async () => { vi.advanceTimersByTime(UNDO_MS + 500) })
    expect(commit).not.toHaveBeenCalled()
  })

  it('keeps the card answerable and says so when the save fails', async () => {
    const block = { ...get('approval'), reversible: false }
    render(<ApprovalCard block={block} commit={() => Promise.reject(new Error('Conflict'))} />)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /Allow once/ })) })
    expect(screen.getByRole('alert').textContent).toContain('Conflict')
    expect(screen.getByRole('button', { name: /Deny/ })).toBeTruthy()
  })

  it('a request that needs a typed phrase links to it in Approvals', async () => {
    const block = { ...get('approval'), reversible: false, itemId: 'item 1' }
    const commit = () => Promise.reject(new Error('This needs a typed confirmation. Open it in Approvals.'))
    render(<MemoryRouter><ApprovalCard block={block} commit={commit} /></MemoryRouter>)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /Allow once/ })) })
    expect(screen.getByRole('link', { name: /Open in Approvals/ }).getAttribute('href')).toBe('/approvals?item=item%201')
  })

  it('an approval that already ran says it cannot be undone', async () => {
    const block = { ...get('approval'), reversible: false }
    render(<ApprovalCard block={block} commit={() => Promise.resolve()} />)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /Allow once/ })) })
    expect(screen.getByRole('status').textContent).toContain('cannot be undone')
    expect(screen.queryByRole('button', { name: /Undo/ })).toBeNull()
  })

  it('shows plan progress as text', () => {
    render(<PlanCard block={get('plan')} />)
    expect(screen.getByRole('progressbar', { name: '5 of 8 done' })).toBeTruthy()
  })
})

describe('run card', () => {
  it('finished: one line with Output and Close; running: live log with Stop', () => {
    const [done, live] = demoBlocks(0).filter((b) => b.kind === 'run') as Extract<StreamBlock, { kind: 'run' }>[]
    const onStop = vi.fn()
    render(<RunCard run={done.run} />)
    expect(screen.getByText('✓ exit 0 · 2.1s')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Output/ }))
    expect(screen.getByRole('log').textContent).toContain('no problems')
    cleanup()
    render(<RunCard run={live.run} onStop={onStop} />)
    fireEvent.click(screen.getByRole('button', { name: /Stop/ }))
    expect(onStop).toHaveBeenCalled()
    expect(screen.getByRole('checkbox', { name: 'Close when done' }).getAttribute('aria-checked')).toBe('true')
  })
})

describe('live mapping', () => {
  it('maps run records to run cards and hides proposals', async () => {
    const { runToBlock, planToBlock } = await import('./useBlocks')
    const done = runToBlock({ run_id: 'r', machine: 'dev-box', command: 'ls', state: 'finished', exit_code: 0, duration_ms: 2140, approval: { mode: 'policy' }, started_at: 5 })
    expect(done).toMatchObject({ kind: 'run', run: { state: 'finished', seconds: 2.1, approval: 'policy', exitCode: 0 } })
    expect(runToBlock({ run_id: 'r2', command: 'x', state: 'pending_approval' })).toBeNull()
    const plan = planToBlock({ id: 'p', steps: [{ id: 'a', title: 'A', status: 'done', position: 0 }, { id: 'b', title: 'B', status: 'doing', position: 1 }, { id: 'c', title: 'C', status: 'todo', position: 2 }, { id: 'd', title: 'D', status: 'cancelled', position: 3 }] })
    expect(plan).toMatchObject({ kind: 'plan', total: 3, done: 1, doing: 1 })
    expect(planToBlock({ id: 'p', steps: [] })).toBeNull()
  })
})
