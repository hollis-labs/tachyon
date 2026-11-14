import { afterEach, expect, it } from 'vitest'
import { DEFAULT_PANEL, panelsFor, registerPanel, resolvePanel, storePanel, storedPanel } from './registry'

const off: (() => void)[] = []
afterEach(() => { off.splice(0).forEach((f) => f()) })

it('the existing tabs are the default Agent Panel', () => {
  expect(panelsFor({})).toEqual([DEFAULT_PANEL])
  expect(DEFAULT_PANEL.tabs.map((t) => t.id)).toEqual(['plan', 'topics', 'bookmarks', 'work', 'activity', 'approvals', 'files', 'doc'])
})

it('a second panel registers, is offered only where it applies, and can be selected', () => {
  off.push(registerPanel({ id: 'pm', label: 'Planner', owner: 'system', tabs: [{ id: 'pm-board', label: 'Board' }], appliesTo: { agents: ['pm-agent'] } }))
  expect(panelsFor({ agentId: 'other' }).map((p) => p.id)).toEqual(['agent'])
  expect(panelsFor({ agentId: 'pm-agent' }).map((p) => p.id)).toEqual(['agent', 'pm'])
  expect(resolvePanel({ agentId: 'pm-agent' }, null).id).toBe('pm')
  expect(resolvePanel({ agentId: 'pm-agent' }, 'agent').id).toBe('agent')
  expect(resolvePanel({ agentId: 'other' }, 'pm').id).toBe('agent')
})

it('the panel pick persists per thread', () => {
  storePanel('t1', 'pm')
  expect(storedPanel('t1')).toBe('pm')
  expect(storedPanel('t2')).toBeNull()
})

it('the default id is reserved', () => {
  expect(() => registerPanel({ id: 'agent', label: 'x', owner: 'user', tabs: [] })).toThrow()
})
