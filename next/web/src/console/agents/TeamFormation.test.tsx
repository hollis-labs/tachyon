import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { TeamFormation } from './TeamFormation'
import { createTeamActionsClient, type FormationReceipt } from './team-actions'
import type { OwnedTeam } from './owned-teams'

afterEach(cleanup)
const team: OwnedTeam = { id: '11111111-1111-4111-8111-111111111111', kind: 'team', ownership: 'tachyon', name: 'Delivery', revision: 3, definition: { name: 'Delivery' } }
function harness() {
  const rows = new Map<string, FormationReceipt>()
  let losePreview = false, loseApply = false
  const fetcher = vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), 'http://localhost')
    const verb = url.pathname.split('/').at(-1)!
    const args = JSON.parse(init?.body as string || url.searchParams.get('input') || '{}')
    const response = (data: unknown) => new Response(JSON.stringify({ version: 1, provider: 'tether', verb, data }))
    if (verb === 'teams.preview') {
      const row: FormationReceipt = { id: args.id, object_id: args.object_id, object_revision: args.expected_revision, provider: 'tether', target: 'https://tether.test', verb: 'teams.form',
        payload: { team: { id: 'tachyon-formation-' + args.id, name: 'Delivery', version: 1, slots: [{ name: 'worker', min: 1, definition_ref: { id: 'worker', revision: 'v2', digest: 'sha256:pin' }, workspace: { mission: args.mission } }] } },
        digest: 'sha256:reviewed', state: 'prepared', attempts: 0, result: {}, created_at: '2026-10-19T00:00:00Z', updated_at: '2026-10-19T00:00:00Z' }
      rows.set(row.id, row)
      if (losePreview) { losePreview = false; throw new Error('Preview response lost') }
      return response(row)
    }
    if (verb === 'actions.list') return response({ items: [...rows.values()] })
    const row = rows.get(args.id)!
    if (verb === 'actions.get') return response(row)
    if (verb === 'teams.apply') {
      if (args.digest !== row.digest) throw new Error('Unreviewed payload')
      row.attempts++
      if (loseApply) { loseApply = false; row.state = 'unknown'; throw new Error('Apply response lost') }
      row.state = 'succeeded'; row.result = { run: { id: 'run-1', status: 'running' } }
      return response(row)
    }
    throw new Error('Unexpected verb ' + verb)
  })
  return { client: createTeamActionsClient(fetcher), fetcher, rows, losePreview: () => { losePreview = true }, loseApply: () => { loseApply = true } }
}
function preview() {
  fireEvent.change(screen.getByLabelText('Formation mission'), { target: { value: 'Review the implementation' } })
  fireEvent.click(screen.getByRole('button', { name: 'Preview formation' }))
}
const reviewed = () => fireEvent.click(screen.getByRole('checkbox', { name: /I reviewed this request/ }))

it('previews a saved revision, requires review, and records a distinct runtime receipt', async () => {
  const h = harness(); render(<TeamFormation team={team} client={h.client} />)
  expect(h.fetcher).not.toHaveBeenCalled() // history is lazy
  preview(); await screen.findByText('Ready for review · saved revision 3')
  expect((screen.getByRole('button', { name: 'Form team in Tether' }) as HTMLButtonElement).disabled).toBe(true)
  expect(h.fetcher.mock.calls.some(([url]) => String(url).endsWith('teams.apply'))).toBe(false)
  reviewed(); fireEvent.click(screen.getByRole('button', { name: 'Form team in Tether' }))
  await screen.findByText('Formation confirmed · saved revision 3')
  expect(screen.getByText(/Run run-1.*reported status running/)).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Form team in Tether' })).toBeNull()
  expect(h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('teams.apply'))).toHaveLength(1)
})

it('recovers a lost preview with a read and preserves the mission', async () => {
  const h = harness(); h.losePreview(); render(<TeamFormation team={team} client={h.client} />)
  preview(); await screen.findByRole('alert')
  expect((screen.getByRole('button', { name: 'Preview formation' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'Check retained preview' }))
  await screen.findByText('Ready for review · saved revision 3')
  expect(screen.getByText('Mission: Review the implementation')).toBeTruthy()
  expect(h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('teams.preview'))).toHaveLength(1)
})

it('does not replay an uncertain apply until the receipt is refreshed and the same request is explicitly reviewed again', async () => {
  const h = harness(); h.loseApply(); render(<TeamFormation team={team} client={h.client} />)
  preview(); await screen.findByText('Ready for review · saved revision 3')
  reviewed(); fireEvent.click(screen.getByRole('button', { name: 'Form team in Tether' }))
  await screen.findByRole('alert')
  expect((screen.getByRole('button', { name: 'Form team in Tether' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'Refresh receipt' }))
  await screen.findByText('Outcome unknown · saved revision 3')
  expect((screen.getByRole('button', { name: 'Retry same formation request' }) as HTMLButtonElement).disabled).toBe(true)
  reviewed(); fireEvent.click(screen.getByRole('button', { name: 'Retry same formation request' }))
  await screen.findByText('Formation confirmed · saved revision 3')
  const writes = h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('teams.apply')).map(([, init]) => JSON.parse(init!.body as string))
  expect(writes).toEqual([{ id: [...h.rows.keys()][0], digest: 'sha256:reviewed', retry: false }, { id: [...h.rows.keys()][0], digest: 'sha256:reviewed', retry: true }])
})

it('recovers retained history after remount and disables an initial apply after the saved revision changes', async () => {
  const h = harness(); const view = render(<TeamFormation team={team} client={h.client} />)
  preview(); await screen.findByText('Ready for review · saved revision 3'); reviewed()
  view.rerender(<TeamFormation team={{ ...team, revision: 4 }} client={h.client} />)
  expect((screen.getByRole('button', { name: 'Form team in Tether' }) as HTMLButtonElement).disabled).toBe(true)
  view.unmount(); render(<TeamFormation team={team} client={h.client} />)
  fireEvent.click(screen.getByRole('button', { name: 'Show formation history' }))
  const history = await screen.findByRole('list', { name: 'Formation history' })
  await waitFor(() => expect(history.querySelector('button')).toBeTruthy())
  fireEvent.click(history.querySelector('button')!)
  await screen.findByText('Ready for review · saved revision 3')
  expect((screen.getByRole('button', { name: 'Form team in Tether' }) as HTMLButtonElement).disabled).toBe(true)
})
