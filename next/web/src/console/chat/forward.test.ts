import { expect, it } from 'vitest'
import { catalogAgent } from '../agents/catalog-client'
import { forwardBody, forwardProblem, forwardTargets } from './forward'
import type { ChatAgent } from './roster'

it('routes launch agents through chat while indexing their real URN and runtime session', () => {
  const roster: ChatAgent[] = [{ agent_id: 'architect', display_name: 'Architect', launch_id: 'architect-live', channel: 'inbox', worker: 'worker-1', session: { id: 's-live', state: 'running', last_activity_at: '', at_turn_boundary: true } }]
  const directory = [catalogAgent({ id: 'architect', urn: 'msg://agent/local/architect', roles: ['planner'] }, []), catalogAgent({ id: 'mail', urn: 'msg://agent/local/mail' }, []), catalogAgent({ id: 'disabled', enabled: false, urn: 'msg://agent/local/disabled' }, [])]
  const targets = forwardTargets(roster, directory)
  expect(targets.find(t => t.label === 'Architect')).toMatchObject({ recipient: { chatLaunchId: 'architect-live' }, keywords: expect.arrayContaining(['s-live', 'msg://agent/local/architect', 'planner', 'worker-1']) })
  expect(targets.find(t => t.label === 'mail')?.recipient.address).toBe('msg://agent/local/mail')
  expect(targets.some(t => t.label === 'disabled')).toBe(false)
  expect(targets.filter(t => t.recipient.agentId === 'architect')).toHaveLength(1)
})

it('preserves the full Markdown and provenance even when the UI preview is shortened', () => {
  const original = '# Heading\n\n' + 'Long message with **formatting**.\n'.repeat(100)
  const body = forwardBody({ id: 'm1', threadId: 't', from: 'agent', body: original, at: 1, tools: [], info: { transport: 'tether-chat', sessionId: 's1' } }, 'Architect', 'Please check this.')
  expect(body).toContain(original)
  expect(body).toContain('Session: s1')
  expect(body).toContain('Message: m1')
  expect(body.startsWith('Please check this.')).toBe(true)
})

it('allows longer session forwards while retaining the mailbox cap and bounding encoded requests', () => {
  const session = { id: 'chat', label: 'Agent', recipient: { kind: 'agent' as const, address: 'chat://tether/launch/agent', label: 'Agent', chatLaunchId: 'agent' } }
  expect(forwardProblem(session, 'x'.repeat(8000))).toBeNull()
  expect(forwardProblem(session, '😀'.repeat(16000))).toMatch(/too large/)
  expect(forwardProblem({ ...session, recipient: { ...session.recipient, chatLaunchId: undefined } }, 'x'.repeat(8000))).toMatch(/too large/)
})
