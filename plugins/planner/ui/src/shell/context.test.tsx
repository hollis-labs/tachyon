import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
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
      <Context row={r} comments={[]} onCommented={() => {}} mentionable={[]} links={[]} />
    </PlannerApiProvider>,
  )
  return api
}
// Base UI's checkbox listens to its hidden input.
const tick = () => act(() => { (document.querySelector('input[type=checkbox]') as HTMLInputElement).click() })

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
