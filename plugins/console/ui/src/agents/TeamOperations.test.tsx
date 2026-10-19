import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { TeamOperations } from './TeamOperations'
import { ownedBuilderClient, type ObjectRecord } from './owned-builder-client'
import { providerActionsClient as client, type ActionReceipt, type RosterSnapshot } from './provider-actions'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('displays the saved roster before refreshing and dispatches the selected saved assignment to its run', async () => {
  const team: ObjectRecord = { id: 'team-1', kind: 'team', name: 'Delivery', ownership: 'tachyon', revision: 3, definition: {} }
  const assignment: ObjectRecord = { id: 'assignment-1', kind: 'assignment', name: 'Review changes', ownership: 'tachyon', revision: 4, definition: { team_id: team.id, state: 'draft' } }
  const formation: ActionReceipt = { id: 'formation-1', object_id: team.id, object_revision: 3, target: 'https://tether.test', verb: 'teams.form', payload: { team: { slots: [{ name: 'worker' }] } }, digest: 'pinned', state: 'succeeded', attempts: 1, result: { run: { id: 'run-1' } }, created_at: '2026-10-19T00:00:00Z', updated_at: '2026-10-19T00:00:00Z' }
  vi.spyOn(ownedBuilderClient, 'cached').mockReturnValue(undefined)
  vi.spyOn(ownedBuilderClient, 'list').mockImplementation(async kind => ({ items: kind === 'team' ? [team] : [assignment] }))
  vi.spyOn(ownedBuilderClient, 'get').mockResolvedValue(team)
  vi.spyOn(client, 'list').mockResolvedValue({ items: [formation] })
  vi.spyOn(client, 'get').mockResolvedValue(formation)
  const snapshot: RosterSnapshot = { runs: [{ run: { id: 'run-1', status: 'active' }, roster_version: 2, members: [{ id: 'member-1', slot: 'worker', status: 'active', actor: 'worker-agent', kind: 'agent' }] }], observed_at: '2026-10-19T00:00:00Z' }
  let refreshed!: (value: RosterSnapshot) => void
  const pending = new Promise<RosterSnapshot>(resolve => { refreshed = resolve })
  const roster = vi.spyOn(client, 'roster').mockImplementation(async (_id, refresh) => refresh ? pending : snapshot)
  const preview = vi.spyOn(client, 'runtime').mockImplementation(async input => ({ ...formation, id: input.id, object_id: input.object_id, object_revision: input.expected_revision, verb: 'teams.assign', state: 'prepared', payload: { body: input.body } }))
  const apply = vi.spyOn(client, 'apply')
  render(<TeamOperations />)
  await screen.findByRole('option', { name: 'Delivery' }); fireEvent.change(screen.getByLabelText('Saved team'), { target: { value: team.id } })
  await screen.findByRole('option', { name: /formation-1/ }); fireEvent.change(screen.getByLabelText('Confirmed formation'), { target: { value: formation.id } })
  await screen.findByText('worker · member-1 · active · worker-agent')
  expect(screen.getByText(/Refreshing in the background/)).toBeTruthy()
  expect(roster.mock.calls).toEqual([[formation.id], [formation.id, true]])
  await act(async () => { refreshed({ ...snapshot, problem: 'Refresh unavailable; saved membership retained.' }) })
  expect(screen.getByText('worker · member-1 · active · worker-agent')).toBeTruthy()
  fireEvent.change(screen.getByLabelText('Team operation'), { target: { value: 'assign' } })
  await screen.findByRole('option', { name: 'Review changes · revision 4' })
  fireEvent.change(screen.getByLabelText('Saved assignment'), { target: { value: assignment.id } })
  expect(screen.queryByRole('option', { name: 'Whole team' })).toBeNull()
  fireEvent.change(screen.getByLabelText('Team address'), { target: { value: '@worker' } })
  fireEvent.change(screen.getByLabelText('Message or assignment briefing'), { target: { value: 'Review carefully' } })
  fireEvent.click(screen.getByRole('button', { name: 'Preview provider action' }))
  await waitFor(() => expect(preview).toHaveBeenCalledWith(expect.objectContaining({ object_id: assignment.id, expected_revision: 4, formation_id: formation.id, operation: 'assign', address: '@worker', body: 'Review carefully' })))
  expect(apply).not.toHaveBeenCalled()
})
