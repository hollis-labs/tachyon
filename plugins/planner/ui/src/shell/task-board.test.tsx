import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import { PlannerApiError } from '@planner/api/errors'
import type { ThemeTask, ThemeTasks } from '@planner/api/types'
import { TaskBoard } from './task-board'

// Dragging is dnd-kit's job (shared.test); here a button per card calls the host's onMove.
vi.mock('@planner/shared', async (orig) => ({
  ...(await orig<typeof import('@planner/shared')>()),
  LaneBoard: ({ lanes, onMove }: { lanes: { id: string; cards: { id: string; content: React.ReactNode; pending?: boolean }[] }[]; onMove: (id: string, from: string, to: string) => void }) => (
    <div>
      {lanes.map((l) => (
        <ul key={l.id} aria-label={l.id}>
          {l.cards.map((c) => (
            <li key={c.id} data-pending={c.pending ? 'yes' : undefined}>
              {c.content}
              {['review', 'blocked', 'done'].map((to) => <button key={to} onClick={() => onMove(c.id, l.id, to)}>{`${c.id} to ${to}`}</button>)}
            </li>
          ))}
        </ul>
      ))}
    </div>
  ),
}))
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }))
import { toast } from 'sonner'

afterEach(() => { cleanup(); vi.clearAllMocks() })

const task = (id: string, status: ThemeTask['status'], over: Partial<ThemeTask> = {}): ThemeTask => ({ id, title: `Task ${id}`, status, updated_at: '2026-10-30T10:00:00Z', assignees: [], pr_cites: [], ...over })
const tasks = (): ThemeTasks => ({
  theme_id: 't1',
  counts: { todo: 0, doing: 1, review: 1, blocked: 0, done: 0, total: 2 },
  lanes: {
    todo: [], doing: [task('A', 'doing', { assignees: [{ assignment_id: 'x', agent_id: 'builder', state: 'active' }] })], blocked: [], done: [],
    review: [task('B', 'review', { pr_cites: [{ host: 'github.com', owner: 'hollis-labs', repo: 'tachyon', number: 229, source: 'detected' }] })],
  },
})

function setup(over: Record<string, unknown> = {}, onNeedsAccess?: () => void) {
  const api = {
    provider: 'tachyon',
    subscribeEvents: () => () => {},
    plannerThemeTasks: vi.fn(async () => tasks()),
    plannerTaskTransition: vi.fn(async (id: string, to: string, from: string) => ({ task: { id, status: to, previous: from }, undo: { to: from, expected_status: to, window_seconds: 8 } })),
    plannerTaskMeta: vi.fn(async () => ({})),
    ...over,
  }
  render(<PlannerApiProvider client={api as never}><TaskBoard themeId="t1" onNeedsAccess={onNeedsAccess} /></PlannerApiProvider>)
  return api
}
const lane = (id: string) => screen.getByRole('list', { name: id })

describe('TaskBoard', () => {
  it('shows the theme\'s tasks by lane with PR chips and assignees', async () => {
    setup()
    expect(await screen.findByText('Task A')).toBeTruthy()
    expect(within(lane('review')).getByRole('link', { name: 'PR 229' }).getAttribute('href')).toBe('https://github.com/hollis-labs/tachyon/pull/229')
    expect(within(lane('doing')).getByText('builder')).toBeTruthy()
  })
  it('moves a card at once, sends the transition with the status it was in, and offers Undo as the inverse', async () => {
    const api = setup()
    await screen.findByText('Task A')
    fireEvent.click(screen.getByRole('button', { name: 'A to review' }))
    expect(within(lane('review')).getByText('Task A')).toBeTruthy()
    await waitFor(() => expect(api.plannerTaskTransition).toHaveBeenCalledWith('A', 'review', 'doing', undefined))
    const undo = (toast as unknown as ReturnType<typeof vi.fn>).mock.calls[0][1].action
    undo.onClick()
    await waitFor(() => expect(api.plannerTaskTransition).toHaveBeenLastCalledWith('A', 'doing', 'review', undefined))
  })
  it('puts the card back and asks the shell to turn writes off when operator access is missing, without a toast per try', async () => {
    const needs = vi.fn()
    setup({ plannerTaskTransition: vi.fn().mockRejectedValue(new PlannerApiError(403, 'Unlock required', 'assurance_insufficient')) }, needs)
    await screen.findByText('Task A')
    fireEvent.click(screen.getByRole('button', { name: 'A to review' }))
    await waitFor(() => expect(needs).toHaveBeenCalledTimes(1))
    expect(within(lane('doing')).getByText('Task A')).toBeTruthy()
    expect((toast as unknown as { error: ReturnType<typeof vi.fn> }).error).not.toHaveBeenCalled()
  })
  it('snaps the card to the status Torque has now on a 409', async () => {
    const api = setup({ plannerTaskTransition: vi.fn().mockRejectedValue(new PlannerApiError(409, 'changed', 'conflict', { current: { id: 'A', status: 'blocked' } })) })
    await screen.findByText('Task A')
    fireEvent.click(screen.getByRole('button', { name: 'A to review' }))
    await waitFor(() => expect(within(lane('blocked')).getByText('Task A')).toBeTruthy())
    expect((toast as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0]).toBe('Task A changed elsewhere and is in Blocked')
    expect(api.plannerTaskTransition).toHaveBeenCalledTimes(1)
  })
  it('asks for a reason before blocking and sends it', async () => {
    const api = setup()
    await screen.findByText('Task A')
    fireEvent.click(screen.getByRole('button', { name: 'A to blocked' }))
    expect(api.plannerTaskTransition).not.toHaveBeenCalled()
    expect((screen.getByRole('button', { name: 'Block' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByLabelText('Why is it blocked?'), { target: { value: 'Waiting on the API' } })
    fireEvent.click(screen.getByRole('button', { name: 'Block' }))
    await waitFor(() => expect(api.plannerTaskTransition).toHaveBeenCalledWith('A', 'blocked', 'doing', { reason: 'Waiting on the API' }))
  })
  it('saves Done at once and attaches "what shipped" afterwards', async () => {
    const api = setup()
    await screen.findByText('Task B')
    fireEvent.click(screen.getByRole('button', { name: 'B to done' }))
    await waitFor(() => expect(api.plannerTaskTransition).toHaveBeenCalledWith('B', 'done', 'review', undefined))
    fireEvent.change(await screen.findByLabelText('What shipped?'), { target: { value: 'Usage index' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(api.plannerTaskMeta).toHaveBeenCalledWith('B', { done_summary: 'Usage index' }))
    expect(await screen.findByText('Usage index')).toBeTruthy()
  })
  it('offers Retry when the tasks cannot be loaded', async () => {
    const fail = vi.fn().mockRejectedValueOnce(new Error('down')).mockResolvedValue(tasks())
    setup({ plannerThemeTasks: fail })
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Task A')).toBeTruthy()
  })
  it('says the lanes are not available when the backend has no such route yet', async () => {
    setup({ plannerThemeTasks: vi.fn().mockRejectedValue(new PlannerApiError(404, 'not found', 'not_found')) })
    expect(await screen.findByText(/not available on this backend yet/)).toBeTruthy()
  })
  it('has a Move to menu on every card, so drag is never the only way', async () => {
    const api = setup()
    await screen.findByText('Task A')
    fireEvent.click(screen.getByRole('button', { name: 'Move Task A. Now in Doing' }))
    await act(async () => {})
    fireEvent.click(screen.getByRole('menuitem', { name: /Review/ }))
    await waitFor(() => expect(api.plannerTaskTransition).toHaveBeenCalledWith('A', 'review', 'doing', undefined))
  })
  it('shows one lane with a switcher on a narrow screen', async () => {
    const mm = vi.fn().mockReturnValue({ matches: true, addEventListener: () => {}, removeEventListener: () => {} })
    vi.stubGlobal('matchMedia', mm)
    window.matchMedia = mm as never
    setup()
    await screen.findByText('Task A')
    expect(screen.queryByRole('list', { name: 'review' })).toBeNull()
    fireEvent.click(screen.getByRole('tab', { name: /Review 1/ }))
    expect(screen.getByRole('list', { name: 'review' })).toBeTruthy()
    vi.unstubAllGlobals()
    // @ts-expect-error restore jsdom default
    delete window.matchMedia
  })
})
