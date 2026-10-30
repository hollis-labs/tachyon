import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import LensPage from '@planner/shell/lens-page'
import { LinkKindPicker } from '@planner/shell/link-picker'
import type { PlannerDb, PlannerItem } from '@planner/api/types'

afterEach(cleanup)

const item = (over: Partial<PlannerItem> & { id: string; title: string }): PlannerItem => ({ rev: 4, ...over })

/** A client double with only what the lens pages call. */
function client(db: PlannerDb, items: PlannerItem[], enums: Record<string, string[]> = {}, extra: Record<string, unknown> = {}) {
  return {
    provider: 'torque',
    subscribeEvents: () => () => {},
    plannerSchema: vi.fn(async () => ({ db, id: 'x', description: '', required: [], fields: {}, enums, link_kinds: [] })),
    plannerList: vi.fn(async () => items),
    plannerUpdate: vi.fn(async () => items[0]),
    plannerDecide: vi.fn(async () => items[0]),
    plannerInboxPromote: vi.fn(async (id: string, to: string, _rev: number, target: string) => ({ inbox: items[0], item: { id: `${to}-${target.slice(0, 4)}`, title: 't', rev: 1 } })),
    plannerInboxDismiss: vi.fn(async () => items[0]),
    ...extra,
  }
}

const page = (api: ReturnType<typeof client>, db: PlannerDb, id?: string) =>
  render(
    <MemoryRouter initialEntries={[`/planner/${db}${id ? `/${id}` : ''}`]}>
      <PlannerApiProvider client={api as never}>
        <Routes>
          <Route path="/planner/:db/:id?" element={<LensPage db={db} />} />
        </Routes>
      </PlannerApiProvider>
    </MemoryRouter>,
  )

describe('Decisions lens', () => {
  const decision = item({ id: 'DEC-1', title: 'Pick one', status: 'needs-decision', options: [{ id: 'a', label: 'Option A' }, { id: 'b', label: 'Option B' }] })

  it('records the choice with the displayed revision, once, even on a double click', async () => {
    const api = client('decisions', [decision])
    api.plannerDecide.mockImplementation(() => new Promise((r) => setTimeout(() => r(decision), 20)))
    page(api, 'decisions', 'DEC-1')
    const choose = await screen.findByRole('button', { name: 'Choose Option A' })
    fireEvent.click(choose)
    fireEvent.click(choose)
    await waitFor(() => expect(api.plannerDecide).toHaveBeenCalledTimes(1))
    expect(api.plannerDecide).toHaveBeenCalledWith('DEC-1', 'a', 4)
  })

  it('reloads after a refused write so the current revision shows, and does not retry by itself', async () => {
    const api = client('decisions', [decision])
    api.plannerDecide.mockRejectedValueOnce(new Error('conflict'))
    page(api, 'decisions', 'DEC-1')
    fireEvent.click(await screen.findByRole('button', { name: 'Choose Option B' }))
    await waitFor(() => expect(api.plannerList).toHaveBeenCalledTimes(2))
    expect(api.plannerDecide).toHaveBeenCalledTimes(1)
  })
})

describe('Roadmap lens', () => {
  it('writes the horizon with the item revision', async () => {
    const api = client('roadmap', [item({ id: 'RM-1', title: 'Thing', status: 'idea', horizon: 'now' })], { horizon: ['now', 'next', 'later'], status: ['idea'] })
    page(api, 'roadmap', 'RM-1')
    fireEvent.click(await screen.findByRole('tab', { name: 'next' }))
    await waitFor(() => expect(api.plannerUpdate).toHaveBeenCalledWith('roadmap', 'RM-1', { horizon: 'next' }, 4))
  })
})

describe('Ideas lens', () => {
  it('saves notes on blur only when they changed', async () => {
    const api = client('ideas', [item({ id: 'ID-1', title: 'Idea', status: 'new', notes: 'old' })])
    page(api, 'ideas', 'ID-1')
    const box = await screen.findByLabelText('Notes')
    // The notes draft resets when the item (re)loads; edit only once it holds the stored value.
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe('old'))
    fireEvent.blur(box)
    expect(api.plannerUpdate).not.toHaveBeenCalled()
    fireEvent.change(box, { target: { value: 'new text' } })
    fireEvent.blur(box)
    await waitFor(() => expect(api.plannerUpdate).toHaveBeenCalledWith('ideas', 'ID-1', { notes: 'new text' }, 4))
  })
})

describe('Inbox lens', () => {
  const card = item({ id: 'IN-1', title: 'Look at this', status: 'new' })

  it('promotes with the displayed revision and reuses the target id when a retry follows a failure', async () => {
    const api = client('inbox', [card])
    api.plannerInboxPromote.mockRejectedValueOnce(new Error('network'))
    page(api, 'inbox')
    const stack = (await screen.findByRole('heading', { name: 'Look at this' })).closest('[tabindex="0"]') as HTMLElement
    fireEvent.keyDown(stack, { key: 't' })
    await waitFor(() => expect(api.plannerInboxPromote).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(api.plannerList).toHaveBeenCalledTimes(2))
    fireEvent.keyDown(stack, { key: 't' })
    await waitFor(() => expect(api.plannerInboxPromote).toHaveBeenCalledTimes(2))
    const [first, second] = api.plannerInboxPromote.mock.calls
    expect(first.slice(0, 3)).toEqual(['IN-1', 'themes', 4])
    expect(second[3]).toBe(first[3])
  })

  it('dismisses with the displayed revision', async () => {
    const api = client('inbox', [card])
    page(api, 'inbox')
    const stack = (await screen.findByRole('heading', { name: 'Look at this' })).closest('[tabindex="0"]') as HTMLElement
    fireEvent.keyDown(stack, { key: 'x' })
    await waitFor(() => expect(api.plannerInboxDismiss).toHaveBeenCalledWith('IN-1', 4))
  })

  it('says everything is triaged when nothing is new', async () => {
    page(client('inbox', []), 'inbox')
    expect(await screen.findByText('All triaged')).toBeTruthy()
  })
})

describe('LinkKindPicker', () => {
  it('picks a kind with a number key and cancels with Escape', () => {
    const onPick = vi.fn()
    const onCancel = vi.fn()
    render(<LinkKindPicker open from="A" to="B" onPick={onPick} onCancel={onCancel} />)
    fireEvent.keyDown(window, { key: '2' })
    expect(onPick).toHaveBeenCalledWith('delivers')
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
  })
})
