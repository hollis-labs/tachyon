import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ProviderActionReview } from './ProviderActionReview'
import { createProviderActionsClient, type ActionReceipt } from './provider-actions'

afterEach(cleanup)
function harness(verb = 'teams.address') {
  const rows = new Map<string, ActionReceipt>()
  let lost = false
  const fetcher = vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), 'http://localhost'), action = url.pathname.split('/').at(-1)!
    const args = JSON.parse(init?.body as string || url.searchParams.get('input') || '{}')
    const response = (data: unknown) => new Response(JSON.stringify({ version: 1, provider: 'tether', verb: action, data }))
    if (action === 'runtime.preview' || action === 'agents.publish_preview' || action === 'catalog.apply_preview') {
      const row: ActionReceipt = { id: args.id, object_id: 'owned-1', object_revision: 3, target: 'https://tether.test', verb: action === 'catalog.apply_preview' ? 'catalog.apply' : verb,
        payload: { run_id: 'run-1', body: 'Reviewed request' }, digest: 'sha256:retained', state: 'prepared', attempts: 0, result: {}, created_at: '2026-10-19T00:00:00Z', updated_at: '2026-10-19T00:00:00Z' }
      rows.set(row.id, row); return response(row)
    }
    if (action === 'actions.list') return response({ items: [...rows.values()] })
    const row = rows.get(args.id)!
    if (action === 'actions.get') return response(row)
    if (action === 'actions.reconcile') { row.state = 'succeeded'; row.result = { applied: true, recovered: true }; return response(row) }
    if (action === 'actions.apply') {
      expect(args.digest).toBe(row.digest); row.attempts++
      if (lost) { lost = false; row.state = 'unknown'; throw new Error('Response lost') }
      row.state = 'succeeded'; row.result = row.verb === 'catalog.plan' ? { plan_id: 'cp_123', validation_ok: true, diff: 'exact diff', digest: 'pinned' } : { delivered: true }
      return response(row)
    }
    throw new Error('Unexpected request ' + action)
  })
  const client = createProviderActionsClient(fetcher)
  const preview = (id: string) => verb === 'catalog.plan' ? client.publish({ id, object_id: 'owned-1', expected_revision: 3, provider: 'provider', extends: 'base' }) : client.runtime({ id, object_id: 'owned-1', expected_revision: 3, formation_id: 'formation-1', operation: 'address', address: '@worker', body: 'Reviewed request' })
  return { client, preview, fetcher, rows, lose: () => { lost = true } }
}
const click = (name: string) => fireEvent.click(screen.getByRole('button', { name }))
const review = () => fireEvent.click(screen.getByRole('checkbox', { name: /I reviewed this exact request/ }))

it('retains a failed runtime request and requires readback plus explicit same-key recovery', async () => {
  const h = harness(); h.lose(); render(<ProviderActionReview objectID="owned-1" client={h.client} preview={h.preview}><input aria-label="Draft" defaultValue="keep me" /></ProviderActionReview>)
  expect(h.fetcher).not.toHaveBeenCalled()
  click('Preview provider action'); await screen.findByText(/teams.address · prepared/)
  expect((screen.getByRole('button', { name: 'Apply to Tether' }) as HTMLButtonElement).disabled).toBe(true)
  expect((screen.getByLabelText('Draft') as HTMLInputElement).closest('fieldset')?.disabled).toBe(true)
  review(); click('Apply to Tether'); await screen.findByRole('alert')
  expect((screen.getByRole('button', { name: 'Apply to Tether' }) as HTMLButtonElement).disabled).toBe(true)
  click('Refresh receipt'); await screen.findByText(/teams.address · unknown/)
  review(); click('Retry same request'); await screen.findByText(/teams.address · succeeded/)
  const writes = h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('actions.apply')).map(([, init]) => JSON.parse(init!.body as string))
  expect(writes).toEqual([{ id: [...h.rows.keys()][0], digest: 'sha256:retained', retry: false }, { id: [...h.rows.keys()][0], digest: 'sha256:retained', retry: true }])
  expect((screen.getByLabelText('Draft') as HTMLInputElement).value).toBe('keep me')
})

it('stages catalog installation separately and reconciles an unknown apply without retrying', async () => {
  const h = harness('catalog.plan'); render(<ProviderActionReview objectID="owned-1" client={h.client} preview={h.preview} />)
  click('Preview provider action'); await screen.findByText(/catalog.plan · prepared/)
  review(); click('Create Tether plan'); await screen.findByText(/catalog.plan · succeeded/)
  expect(h.fetcher.mock.calls.some(([url]) => String(url).endsWith('catalog.apply_preview'))).toBe(false)
  click('Preview installation'); await screen.findByText(/catalog.apply · prepared/)
  h.lose(); review(); click('Install approved plan'); await screen.findByRole('alert')
  click('Refresh receipt'); await screen.findByText(/catalog.apply · unknown/)
  expect(screen.queryByRole('button', { name: 'Retry same request' })).toBeNull()
  click('Reconcile installation'); await screen.findByText(/catalog.apply · succeeded/)
  expect(h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('actions.apply'))).toHaveLength(2)
  expect(h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('actions.reconcile'))).toHaveLength(1)
})
