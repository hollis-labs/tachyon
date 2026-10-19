/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup } from '@testing-library/react'
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
