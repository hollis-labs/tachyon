import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TeamsTab } from './teams-tab'
import { ownedBuilderClient, type ObjectRecord } from '../agents/owned-builder-client'
import { providerActionsClient as provider } from '../agents/provider-actions'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
const team = (id: string, name: string, slots: string[]): ObjectRecord => ({ id, kind: 'team', ownership: 'tachyon', name, revision: 1, definition: { slots: slots.map((n) => ({ name: n })) } })

it('groups teams by run state, shows members, and never guesses a run state it could not read', async () => {
  vi.spyOn(ownedBuilderClient, 'cached').mockReturnValue(undefined)
  vi.spyOn(ownedBuilderClient, 'list').mockResolvedValue({ items: [team('a', 'Delivery', ['lead']), team('b', 'Docs', []), team('c', 'Broken', ['x'])] })
  vi.spyOn(provider, 'list').mockImplementation(async (id) => {
    if (id === 'c') throw new Error('Tether unreachable')
    return { items: id === 'a' ? [{ id: 'f1', verb: 'teams.form', object_revision: 1, state: 'succeeded', created_at: '2026-10-31T08:00:00Z' }] : [] }
  })
  vi.spyOn(provider, 'roster').mockResolvedValue({ runs: [{ run: { id: 'r', status: 'active' }, roster_version: 1, members: [{ id: 'm1', slot: 'lead', status: 'active', actor: 'x', kind: 'agent' }] }] })
  render(<MemoryRouter><TeamsTab /></MemoryRouter>)
  expect(await screen.findByText(/1 member/)).toBeTruthy()
  expect(screen.getByText('No confirmed formation yet')).toBeTruthy()
  expect(screen.getAllByText('Run state n/a').length).toBeGreaterThan(0)
  fireEvent.click(screen.getByRole('button', { name: /^Running 1/ }))
  expect(screen.queryByText('Docs')).toBeNull()
  expect(screen.getByText('Delivery')).toBeTruthy()
})
