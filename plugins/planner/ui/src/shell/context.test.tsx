import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PlannerApiError } from '@planner/api/errors'
import { PlannerApiProvider } from '@planner/api/context'
import type { ThemeRow } from '@planner/api/types'
import { Context } from './context'

vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }))
afterEach(cleanup)

const row = (over: Partial<ThemeRow> = {}) => ({ source: 'theme', db: 'themes', id: 't1', rev: 4, title: 'Theme', ...over }) as ThemeRow

function setup(r: ThemeRow) {
  const api = { provider: 'tachyon', subscribeEvents: () => () => {}, plannerComment: vi.fn(async () => ({})) }
  render(
    <PlannerApiProvider client={api as never}>
      <Context db="themes" id={r.id} rev={r.rev} pinned={false} comments={[]} onCommented={() => {}} mentionable={[]} links={[]} />
    </PlannerApiProvider>,
  )
  return api
}
const tick = () => act(() => { fireEvent.click(screen.getByRole('button', { name: 'Pin with this comment' })) })

describe('comment composer pin', () => {
  it('leaves the pin alone on a plain comment', async () => {
    const api = setup(row())
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'hello' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(api.plannerComment).toHaveBeenCalledWith('themes', 't1', 'hello', 4, undefined, undefined))
  })
  it('sends the new pin state with the comment, once', async () => {
    const api = setup(row())
    tick()
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'keep this up' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(api.plannerComment).toHaveBeenCalledWith('themes', 't1', 'keep this up', 4, undefined, { pin: true }))
  })
})


it('preserves a refused comment and uses the displayed revision on explicit retry', async () => {
  const api = { provider: 'tachyon', subscribeEvents: () => () => {}, plannerComment: vi.fn().mockRejectedValueOnce(new PlannerApiError(409, 'Changed', 'conflict')).mockResolvedValue({}) }
  const onCommented = vi.fn()
  const view = (rev: number) => <PlannerApiProvider client={api as never}><Context db="ideas" id="idea-1" rev={rev} pinned={false} comments={[]} onCommented={onCommented} mentionable={[]} links={[]} /></PlannerApiProvider>
  const mounted = render(view(7))
  const box = screen.getByRole('textbox') as HTMLTextAreaElement
  fireEvent.change(box, { target: { value: 'Keep this draft' } })
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(api.plannerComment).toHaveBeenCalledTimes(1))
  await waitFor(() => expect((screen.getByRole('button', { name: 'Send' }) as HTMLButtonElement).disabled).toBe(false))
  expect(box.value).toBe('Keep this draft')
  expect(onCommented).not.toHaveBeenCalled()
  mounted.rerender(view(8))
  expect(api.plannerComment).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(api.plannerComment).toHaveBeenLastCalledWith('ideas', 'idea-1', 'Keep this draft', 8, undefined, undefined))
  expect(api.plannerComment.mock.calls[0][3]).toBe(7)
  await waitFor(() => expect(box.value).toBe(''))
  expect(onCommented).toHaveBeenCalledTimes(1)
})
