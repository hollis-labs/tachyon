/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AgentDirectoryView } from './AgentDirectory'
import { AgentsClientProvider } from './context'
import type { AgentsClient } from './client'
import { filterAgents, isPrivileged, policySummary } from './filter'
import type { AgentRecord } from './types'

afterEach(cleanup)

const NOW = Date.parse('2026-10-16T05:00:00Z')

// Inline test data (no runtime mock ships with the port).
const agent = (id: string, name: string, over: Partial<AgentRecord> = {}): AgentRecord => ({
  identity: { id, name, roles: [], enabled: true },
  runtime: { provider: 'claude-code' },
  policy: { permissionMode: 'default', scopes: [], rules: [] },
  tools: [],
  skills: [],
  teams: [],
  source: { path: `catalog/agents/${id}.yaml` },
  ...over,
})
const MOCK_AGENTS: AgentRecord[] = [
  agent('release-captain', 'Release Captain', { policy: { permissionMode: 'bypass', scopes: ['session.write', 'agent.write'], rules: [] } }),
  agent('team-reviewer', 'Team Reviewer', { identity: { id: 'team-reviewer', name: 'Team Reviewer', roles: ['reviewer'], enabled: true }, live: { state: 'idle', sessions: 1, lastActivity: NOW } }),
  agent('team-worker', 'Team Worker', { identity: { id: 'team-worker', name: 'Team Worker', roles: ['worker'], enabled: true }, live: { state: 'waiting', sessions: 1, lastActivity: NOW } }),
  agent('lead-proxy', 'Lead Proxy', { runtime: { provider: 'opencode' } }),
]

/** In-memory AgentsClient over the inline records; only the reads the directory needs. */
function createTestClient(agents: AgentRecord[] = MOCK_AGENTS): AgentsClient {
  const none = () => Promise.reject(new Error('not used in this test'))
  return {
    listAgents: (q) => Promise.resolve(filterAgents(agents, q)),
    getAgent: (id) => Promise.resolve(agents.find((a) => a.identity.id === id) ?? null),
    planChange: none,
    getPlan: () => Promise.resolve(null),
    approvePlan: none,
    applyPlan: none,
    rollbackApply: none,
    listAudit: () => Promise.resolve([]),
    planLifecycle: none,
    getRetireImpact: none,
    listTemplates: () => Promise.resolve([]),
    listTeams: () => Promise.resolve([]),
    getTeam: () => Promise.resolve(null),
    listAssignments: () => Promise.resolve([]),
  }
}
const view = (client: AgentsClient) =>
  render(
    <MemoryRouter>
      <AgentsClientProvider client={client}><AgentDirectoryView now={NOW} /></AgentsClientProvider>
    </MemoryRouter>,
  )

it('filters by search and provider and sorts live agents first', () => {
  expect(filterAgents(MOCK_AGENTS, { provider: 'opencode' }).map((a) => a.identity.id)).toEqual(['lead-proxy'])
  expect(filterAgents(MOCK_AGENTS, { search: 'REVIEWER' }).map((a) => a.identity.id)).toEqual(['team-reviewer'])
  expect(filterAgents(MOCK_AGENTS, { role: 'worker' })).toHaveLength(1)
  expect(filterAgents(MOCK_AGENTS)[0].live?.state).toBe('waiting')
})

it('summarises policy and flags privileged agents', () => {
  const rc = MOCK_AGENTS.find((a) => a.identity.id === 'release-captain')!
  expect(isPrivileged(rc)).toBe(true)
  expect(policySummary(rc)).toContain('2 privileged scopes')
  expect(isPrivileged(MOCK_AGENTS.find((a) => a.identity.id === 'team-reviewer')!)).toBe(false)
})

it('lists agents, selects one and shows its detail with a fleet link', async () => {
  view(createTestClient())
  const list = await screen.findByRole('list', { name: 'Agents' })
  expect(within(list).getAllByRole('button')).toHaveLength(MOCK_AGENTS.length)
  fireEvent.click(within(list).getByText('Release Captain'))
  const detail = screen.getByRole('article', { name: 'Release Captain detail' })
  expect(within(detail).getByText('catalog/agents/release-captain.yaml')).toBeTruthy()
  expect(within(detail).getByText('session.write')).toBeTruthy()
  expect(within(detail).getByText('NOT RUNNING')).toBeTruthy()
  expect(within(detail).queryByRole('button')).toBeNull()
  fireEvent.click(within(list).getByText('Team Worker'))
  expect(screen.getByRole('link', { name: 'open in fleet' }).getAttribute('href')).toBe('/agents')
})

it('narrows the list from the search box and shows an empty state', async () => {
  view(createTestClient())
  await screen.findByRole('list', { name: 'Agents' })
  fireEvent.change(screen.getByLabelText('Search agents'), { target: { value: 'zzz' } })
  expect(screen.getByText('No agents match')).toBeTruthy()
})

it('shows an error state when the client fails', async () => {
  const bad: AgentsClient = { ...createTestClient(), listAgents: () => Promise.reject(new Error('boom')), getAgent: () => Promise.resolve(null) }
  view(bad)
  await waitFor(() => expect(screen.getByText('boom')).toBeTruthy())
})
