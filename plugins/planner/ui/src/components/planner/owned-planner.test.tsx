import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import { TachyonPlannerClient } from '@planner/api/client'
import { PlannerApiError } from '@planner/api/errors'
import type { PlannerItem, PlannerSchemaInfo } from '@planner/api/types'
import { PlannerDetailDialog } from './planner-detail-dialog'
import { PlannerCreateDialog } from './planner-create-dialog'
import { operatorElevation } from '@console/operator/elevation'

const id = '019a0000-0000-7000-8000-000000000001'
const schema: PlannerSchemaInfo = { db: 'ideas', id: 'owned/ideas@1', description: '', required: ['title'], fields: { title: { type: 'string' }, notes: { type: 'string' } }, enums: {}, link_kinds: ['url', 'torque', 'planner'] }
const initial: PlannerItem = { id, title: 'Imported idea', rev: 7, status: 'new', notes: 'Original notes', imported_detail: { source_id: 'ID-004', extra: 'Preserved source detail' }, comments: [{ id: 'old-comment', text: 'Imported comment', author: 'Chrispian', created: '2026-10-18' }], links: [{ kind: 'url', ref: 'https://example.com/original', label: 'Original link' }] }
function client() {
  return Object.assign(new TachyonPlannerClient(), {
    plannerGet: vi.fn().mockResolvedValue(initial),
    plannerSchema: vi.fn().mockResolvedValue(schema),
    plannerTaskTitles: vi.fn().mockResolvedValue({}),
  })
}
function detail(api: ReturnType<typeof client>, refreshKey = 0) {
  return <MemoryRouter><PlannerApiProvider client={api}><PlannerDetailDialog db="ideas" id="ID-004" refreshKey={refreshKey} onClose={vi.fn()} onChanged={vi.fn()} onOpen={vi.fn()} /></PlannerApiProvider></MemoryRouter>
}
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('owned Planner detail behavior', () => {
  it('opens a related UUID using the resolved database and preserves the current detail on failure', async () => {
    const api = client()
    const target = '019a0000-0000-7000-8000-000000000002'
    api.plannerGet.mockResolvedValue({ ...initial, related_ids: [target] })
    const resolve = vi.spyOn(api, 'resolveReference').mockRejectedValueOnce(new Error('Reference unavailable')).mockResolvedValueOnce({ db: 'risks', id: target })
    const open = vi.fn()
    render(<MemoryRouter><PlannerApiProvider client={api}><PlannerDetailDialog db="ideas" id={id} onClose={vi.fn()} onChanged={vi.fn()} onOpen={open} /></PlannerApiProvider></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: target }))
    await screen.findByText('Reference unavailable')
    expect(screen.getByText('Imported idea')).toBeTruthy()
    expect(open).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: target }))
    await waitFor(() => expect(open).toHaveBeenCalledWith('risks', target))
    expect(resolve).toHaveBeenLastCalledWith(null, target)
  })
  it('preserves imported detail and links, and writes a comment against the displayed UUID and revision', async () => {
    const api = client()
    const comment = vi.spyOn(api, 'plannerComment').mockResolvedValue({ ...initial, rev: 8 })
    render(detail(api))
    await screen.findByText('Imported idea')
    expect(screen.getByText(/Preserved source detail/)).toBeTruthy()
    expect(screen.getByText('Imported comment')).toBeTruthy()
    expect(screen.getByRole('link', { name: /Original link/ }).getAttribute('href')).toBe('https://example.com/original')
    fireEvent.change(screen.getByLabelText('New comment'), { target: { value: 'New comment' } })
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    await waitFor(() => expect(comment).toHaveBeenCalledWith('ideas', id, 'New comment', 7))
  })
  it('preserves an edit on conflict and sends only authored fields after an explicit reload', async () => {
    const api = client()
    const update = vi.spyOn(api, 'plannerUpdate').mockRejectedValueOnce(new PlannerApiError(409, 'Changed', 'conflict')).mockResolvedValueOnce({ ...initial, title: 'My edit', notes: 'Someone else updated notes', rev: 9 })
    render(detail(api))
    await screen.findByText('Imported idea')
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    fireEvent.change(screen.getByLabelText(/^Title/), { target: { value: 'My edit' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByRole('alert')
    expect((screen.getByLabelText(/^Title/) as HTMLInputElement).value).toBe('My edit')
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true)
    expect(update).toHaveBeenCalledTimes(1)
    api.plannerGet.mockResolvedValue({ ...initial, notes: 'Someone else updated notes', rev: 8 })
    fireEvent.click(screen.getByRole('button', { name: 'Reload latest version' }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(update).toHaveBeenLastCalledWith('ideas', id, { title: 'My edit' }, 8))
  })
  it('does not replace a reviewed revision or discard a draft during background refresh', async () => {
    const api = client()
    const update = vi.spyOn(api, 'plannerUpdate').mockResolvedValue({ ...initial, rev: 8 })
    const view = render(detail(api))
    await screen.findByText('Imported idea')
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    fireEvent.change(screen.getByLabelText(/^Title/), { target: { value: 'My draft' } })
    api.plannerGet.mockResolvedValue({ ...initial, title: 'Changed remotely', rev: 8 })
    view.rerender(detail(api, 1))
    await screen.findByText(/A newer version is available/)
    expect((screen.getByLabelText(/^Title/) as HTMLInputElement).value).toBe('My draft')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(update).toHaveBeenCalledWith('ideas', id, { title: 'My draft' }, 7))
  })
  it('authorizes explicitly without replaying a refused comment', async () => {
    const api = client()
    const comment = vi.spyOn(api, 'plannerComment').mockRejectedValue(new PlannerApiError(403, 'Unlock required', 'assurance_insufficient'))
    const authorize = vi.spyOn(operatorElevation, 'ensure').mockResolvedValue(true)
    render(detail(api))
    await screen.findByText('Imported idea')
    fireEvent.change(screen.getByLabelText('New comment'), { target: { value: 'Keep this draft' } })
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Authorize operator' }))
    await screen.findByText(/Operator access verified/)
    expect(authorize).toHaveBeenCalledTimes(1)
    expect(comment).toHaveBeenCalledTimes(1)
    expect((screen.getByLabelText('New comment') as HTMLTextAreaElement).value).toBe('Keep this draft')
  })
  it('retains the create UUID and draft after an uncertain network outcome', async () => {
    const api = client()
    const create = vi.spyOn(api, 'plannerCreate').mockRejectedValue(new TypeError('Network disconnected'))
    render(<PlannerApiProvider client={api}><PlannerCreateDialog db="ideas" open onOpenChange={vi.fn()} onCreated={vi.fn()} /></PlannerApiProvider>)
    await screen.findByLabelText(/^Title/)
    fireEvent.change(screen.getByLabelText(/^Title/), { target: { value: 'New idea' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create idea' }))
    // findBy's default 1s was too tight on loaded CI runners (file took ~3s).
    await screen.findByRole('alert', {}, { timeout: 5000 })
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Create idea' })) })
    expect(create).toHaveBeenCalledTimes(2)
    expect(create.mock.calls[0]).toEqual(create.mock.calls[1])
    expect(create.mock.calls[0][2]).toMatch(/^[0-9a-f-]{36}$/)
  })
})

describe('decision and inbox actions', () => {
  function mount(api: ReturnType<typeof client>, db: 'decisions' | 'inbox', onOpen = vi.fn()) {
    render(<MemoryRouter><PlannerApiProvider client={api}><PlannerDetailDialog db={db} id={id} onClose={vi.fn()} onChanged={vi.fn()} onOpen={onOpen} /></PlannerApiProvider></MemoryRouter>)
  }
  it.each(['decide', 'defer', 'reopen'] as const)('preserves %s with the reviewed revision', async action => {
    const api = client()
    api.plannerGet.mockResolvedValue({ ...initial, status: action === 'reopen' ? 'accepted' : 'proposed', options: [{ id: 'option-a', label: 'Option A', summary: 'Imported option detail' }] })
    const decide = vi.spyOn(api, 'plannerDecide').mockResolvedValue(initial)
    const defer = vi.spyOn(api, 'plannerDefer').mockResolvedValue(initial)
    const reopen = vi.spyOn(api, 'plannerReopen').mockResolvedValue(initial)
    mount(api, 'decisions')
    await screen.findByText('Imported option detail')
    if (action === 'reopen') {
      fireEvent.click(screen.getByRole('button', { name: 'Reopen' }))
      await waitFor(() => expect(reopen).toHaveBeenCalledWith(id, 7))
    } else {
      fireEvent.change(screen.getByLabelText('Decision comment'), { target: { value: 'Reviewed rationale' } })
      fireEvent.click(screen.getByRole('button', { name: action === 'decide' ? 'Choose option option-a: Option A' : 'Defer' }))
      await waitFor(() => action === 'decide' ? expect(decide).toHaveBeenCalledWith(id, 'option-a', 7, 'Reviewed rationale') : expect(defer).toHaveBeenCalledWith(id, 7, 'Reviewed rationale'))
    }
  })
  it('reuses the promotion target UUID after an uncertain response and opens the returned item', async () => {
    const api = client()
    const promoted = { ...initial, id: '019a0000-0000-7000-8000-000000000002' }
    const promote = vi.spyOn(api, 'plannerInboxPromote').mockRejectedValueOnce(new TypeError('Disconnected')).mockResolvedValueOnce({ inbox: initial, item: promoted })
    const onOpen = vi.fn()
    mount(api, 'inbox', onOpen)
    await screen.findByText('Imported idea')
    fireEvent.click(screen.getByRole('button', { name: 'Promote' }))
    await screen.findByRole('alert')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Promote' })) })
    expect(promote.mock.calls[0]).toEqual(promote.mock.calls[1])
    expect(promote.mock.calls[0].slice(0, 3)).toEqual([id, 'ideas', 7])
    expect(promote.mock.calls[0][3]).toMatch(/^[0-9a-f-]{36}$/)
    expect(onOpen).toHaveBeenCalledWith('ideas', promoted.id)
  })
  it('dismisses an inbox item using the displayed revision', async () => {
    const api = client()
    const dismiss = vi.spyOn(api, 'plannerInboxDismiss').mockResolvedValue({ ...initial, status: 'dismissed', rev: 8 })
    mount(api, 'inbox')
    await screen.findByText('Imported idea')
    fireEvent.change(screen.getByLabelText('Dismiss note'), { target: { value: 'Duplicate' } })
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(dismiss).toHaveBeenCalledWith(id, 7, 'Duplicate'))
  })
})

it('shows the promotion receipt on a triaged native inbox instead of offering promotion again', async () => {
  const api = client()
  api.plannerGet.mockResolvedValue({ ...initial, status: 'triaged', promoted_to: { db: 'ideas', id: '019a0000-0000-7000-8000-000000000002' } })
  render(<MemoryRouter><PlannerApiProvider client={api}><PlannerDetailDialog db="inbox" id={id} onClose={vi.fn()} onChanged={vi.fn()} onOpen={vi.fn()} /></PlannerApiProvider></MemoryRouter>)
  await screen.findByText(/Promoted to/)
  expect(screen.queryByRole('button', { name: 'Promote' })).toBeNull()
})
