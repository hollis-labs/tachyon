import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, it, vi, expect } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import { TachyonPlannerClient } from '@planner/api/client'
import { PlannerCreateDialog } from './planner-create-dialog'

function client() {
  return Object.assign(new TachyonPlannerClient(), {
    plannerSchema: vi.fn().mockResolvedValue({ db: 'ideas', id: 'owned/ideas@1', description: '', required: ['title'], fields: { title: { type: 'string' } }, enums: {}, link_kinds: [] }),
  })
}
afterEach(() => { cleanup(); vi.restoreAllMocks() })

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
