import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { OwnedBuilderView } from './OwnedBuilderView'
import { createOwnedBuilderClient, type ObjectRecord } from './owned-builder-client'
import { builderDraft, builderPatch } from './owned-builder-draft'

afterEach(cleanup)
const agentID = '11111111-1111-4111-8111-111111111111'
const taskID = '22222222-2222-4222-8222-222222222222'
const agent = (): ObjectRecord => ({ id: agentID, kind: 'agent', ownership: 'tachyon', name: 'Worker', revision: 2, definition: { name: 'Worker', enabled: true, instructions: 'Original', properties: { preserve: true } } })
function harness(initial: ObjectRecord[] = [agent()]) {
  const rows = new Map(initial.map(row => [row.id, structuredClone(row)]))
  const fetcher = vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), 'http://localhost')
    const verb = url.pathname.split('/').at(-1)!
    const args = JSON.parse(init?.body as string || url.searchParams.get('input') || '{}')
    const row = rows.get(args.id)
    const result = (data: unknown) => new Response(JSON.stringify({ version: 1, provider: 'tachyon', verb, data }))
    if (verb === 'objects.list') return result({ items: [...rows.values()].filter(r => r.kind === args.kind) })
    if (verb === 'objects.get') return row ? result(row) : new Response('{}', { status: 404 })
    if (verb === 'objects.history') return result({ items: [{ record: row, actor: 'operator:browser', operation: 'patch', recorded_at: 'today' }] })
    if (verb === 'objects.create') {
      const made: ObjectRecord = { id: args.id, kind: args.kind, ownership: 'tachyon', name: args.definition.name, revision: 1, definition: args.definition }
      rows.set(made.id, made); return result(made)
    }
    if (!row || row.revision !== args.expected_revision) return new Response(JSON.stringify({ error: 'conflict' }), { status: 409 })
    const next = structuredClone(row)
    if (verb === 'objects.take_ownership') next.ownership = 'tachyon'
    else if (verb === 'objects.delete') next.deleted_at = 'today'
    else if (verb === 'objects.restore') delete next.deleted_at
    else if (verb === 'objects.patch') {
      for (const [key, value] of Object.entries(args.definition)) {
        if (value === null) delete next.definition[key]
        else next.definition[key] = value
      }
      next.name = String(next.definition.name)
    } else throw new Error('Unexpected operation ' + verb)
    next.revision++; rows.set(next.id, next); return result(next)
  })
  const client = createOwnedBuilderClient(fetcher)
  return { client, fetcher, rows }
}

it('adopts explicitly, saves only reviewed field deltas, and preserves unknown data and conflicting edits', async () => {
  const h = harness([{ ...agent(), ownership: 'source' }])
  render(<OwnedBuilderView kind="agent" client={h.client} />)
  fireEvent.click(await screen.findByRole('button', { name: /Worker.*provider observation/ }))
  expect((await screen.findByLabelText('Instructions') as HTMLTextAreaElement).disabled).toBe(false) // fieldset enforces the lock
  expect(screen.getByRole('button', { name: 'Review agent' }).closest('fieldset')?.disabled).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'Adopt definition into Tachyon' }))
  await screen.findByText(/Tachyon now owns/)
  fireEvent.change(screen.getByLabelText('Instructions'), { target: { value: 'New instructions' } })
  fireEvent.click(screen.getByRole('button', { name: 'Review agent' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save agent' }))
  await screen.findByText('Saved in Tachyon.')
  const write = h.fetcher.mock.calls.find(([url]) => String(url).endsWith('objects.patch'))!
  expect(JSON.parse(write[1]!.body as string)).toEqual({ id: agentID, expected_revision: 3, definition: { instructions: 'New instructions' } })
  expect(h.rows.get(agentID)?.definition.properties).toEqual({ preserve: true })
  fireEvent.change(screen.getByLabelText('Instructions'), { target: { value: 'Keep this draft' } })
  h.rows.get(agentID)!.revision++
  fireEvent.click(screen.getByRole('button', { name: 'Review agent' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save agent' }))
  await screen.findByRole('alert')
  fireEvent.click(screen.getByRole('button', { name: 'Continue editing' }))
  expect((screen.getByLabelText('Instructions') as HTMLTextAreaElement).value).toBe('Keep this draft')
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await screen.findByText(/A newer revision is available/)
  expect((screen.getByLabelText('Instructions') as HTMLTextAreaElement).value).toBe('Keep this draft')
})

it('creates assignments with explicit references and switches local targets to external targets without keeping both', async () => {
  const h = harness([agent(), { id: taskID, kind: 'task', ownership: 'tachyon', name: 'Review work', revision: 1, definition: { name: 'Review work' } }])
  render(<OwnedBuilderView kind="assignment" client={h.client} />)
  fireEvent.click(screen.getByRole('button', { name: 'New assignment' }))
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Review assignment' } })
  await screen.findByRole('option', { name: 'Worker' })
  fireEvent.change(screen.getByLabelText('Assignee'), { target: { value: agentID } })
  await screen.findByRole('option', { name: 'Review work' })
  fireEvent.change(screen.getByLabelText('Assigned work'), { target: { value: taskID } })
  fireEvent.click(screen.getByRole('button', { name: 'Review assignment' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save assignment' }))
  await screen.findByText('Saved in Tachyon.')
  const row = [...h.rows.values()].find(r => r.kind === 'assignment')!
  expect(row.definition).toMatchObject({ agent_id: agentID, state: 'draft', target: { kind: 'task', id: taskID } })
  fireEvent.change(screen.getByLabelText('Target source'), { target: { value: 'external' } })
  for (const [label, value] of [['Provider', 'torque'], ['Instance', 'https://torque.example'], ['Collection', 'tasks'], ['External ID', 'CW-123']]) fireEvent.change(screen.getByLabelText(label), { target: { value } })
  fireEvent.click(screen.getByRole('button', { name: 'Review assignment' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save assignment' }))
  await screen.findByText('Saved in Tachyon.')
  expect(h.rows.get(row.id)?.definition.target).toBeUndefined()
  expect(h.rows.get(row.id)?.definition.external_target).toEqual({ provider: 'torque', instance: 'https://torque.example', collection: 'tasks', external_id: 'CW-123' })
})

it('loads history on demand and retires/restores while retaining identity', async () => {
  const h = harness()
  render(<OwnedBuilderView kind="agent" client={h.client} />)
  fireEvent.click(await screen.findByRole('button', { name: /Worker.*owned/ }))
  await screen.findByLabelText('Instructions')
  expect(h.fetcher.mock.calls.some(([url]) => String(url).includes('objects.history'))).toBe(false)
  fireEvent.click(screen.getByRole('button', { name: 'Load history' }))
  await screen.findByText(/operator:browser/)
  fireEvent.click(screen.getByRole('button', { name: 'Retire agent' }))
  await screen.findByText('Retired. History is retained.')
  fireEvent.click(screen.getByRole('button', { name: 'Restore agent' }))
  await screen.findByText('Restored.')
  expect(h.rows.get(agentID)?.deleted_at).toBeUndefined()
  expect(h.rows.get(agentID)?.revision).toBe(4)
})

it('reuses create identity after uncertain delivery and only retries on an explicit action', async () => {
  const h = harness([])
  const original = h.fetcher.getMockImplementation()!
  let failed = false
  h.fetcher.mockImplementation(async (input, init) => {
    if (String(input).endsWith('objects.create') && !failed) { failed = true; throw new Error('Connection interrupted') }
    return original(input, init)
  })
  render(<OwnedBuilderView kind="agent" client={h.client} />)
  fireEvent.click(screen.getByRole('button', { name: 'New agent' }))
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'New worker' } })
  fireEvent.click(screen.getByRole('button', { name: 'Review agent' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save agent' }))
  await screen.findByText('Connection interrupted')
  const attempts = () => h.fetcher.mock.calls.filter(([url]) => String(url).endsWith('objects.create'))
  expect(attempts()).toHaveLength(1)
  fireEvent.click(screen.getByRole('button', { name: 'Save agent' }))
  await screen.findByText('Saved in Tachyon.')
  expect(attempts()[1][1]!.body).toBe(attempts()[0][1]!.body)
})

it('sends no unrelated fields when a native record has fields the editor does not expose', () => {
  const row = agent()
  row.definition.tags = ['keep']; row.definition.project_id = 'old-project'
  const draft = { ...builderDraft('agent', row), project_id: '' }
  expect(builderPatch('agent', draft, row)).toEqual({ project_id: null })
})

it('checks an uncertain create without discarding edits made after the response was lost', async () => {
  const h = harness([])
  const original = h.fetcher.getMockImplementation()!
  h.fetcher.mockImplementation(async (input, init) => {
    const response = await original(input, init)
    if (String(input).endsWith('objects.create')) throw new Error('Response lost')
    return response
  })
  render(<OwnedBuilderView kind="agent" client={h.client} />)
  fireEvent.click(screen.getByRole('button', { name: 'New agent' }))
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'First name' } })
  fireEvent.click(screen.getByRole('button', { name: 'Review agent' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save agent' }))
  await screen.findByText('Response lost')
  fireEvent.click(screen.getByRole('button', { name: 'Continue editing' }))
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Later edit' } })
  fireEvent.click(screen.getByRole('button', { name: 'Check whether create was saved' }))
  await screen.findByText(/The create was saved/)
  expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Later edit')
  fireEvent.click(screen.getByRole('button', { name: 'Review agent' }))
  fireEvent.click(screen.getByRole('button', { name: 'Save agent' }))
  await screen.findByText('Saved in Tachyon.')
  const write = h.fetcher.mock.calls.find(([url]) => String(url).endsWith('objects.patch'))!
  expect(JSON.parse(write[1]!.body as string)).toMatchObject({ expected_revision: 1, definition: { name: 'Later edit' } })
})

it('deduplicates page reads and shows a cached page before its refresh finishes', async () => {
  const h = harness()
  const a = h.client.list('agent'), b = h.client.list('agent')
  expect(a).toBe(b)
  await a
  let release!: (r: Response) => void
  h.fetcher.mockImplementation(() => new Promise(resolve => { release = resolve }))
  render(<OwnedBuilderView kind="agent" client={h.client} />)
  expect(screen.getByRole('button', { name: /Worker.*owned/ })).toBeTruthy()
  release(new Response(JSON.stringify({ version: 1, provider: 'tachyon', verb: 'objects.list', data: { items: [agent()], next_after_id: agentID } })))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Load more agents' })).toBeTruthy())
})
