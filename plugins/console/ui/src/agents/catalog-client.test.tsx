import { afterEach, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import { createCatalogAgentsClient, DEMO_SAVE_MESSAGE } from './catalog-client'
import { draftFromAgent } from './plan'
import { blankDraft } from './lifecycle'

afterEach(cleanup)

function setup() {
  const fetcher = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method && init.method !== 'GET') throw new Error('Unexpected write')
    const kind = String(url).split('/').pop()
    const body = kind === 'agents' ? { agents: [{ id: 'reviewer', name: 'Reviewer', roles: ['reviewer'], system_prompt: 'System instructions', agent_prompt: 'Agent instructions', permissions: { network: true }, skills: ['search-first'] }] }
      : { launches: [{ id: 'review-code', agent: 'reviewer', provider: 'codex-app-server', project: 'tachyon' }, { id: 'review-docs', agent: 'reviewer', provider: 'claude-code', project: 'docs' }] }
    return new Response(JSON.stringify(body), { status: 200 })
  })
  return { fetcher, client: createCatalogAgentsClient(fetcher) }
}

it('reads catalog agents without collapsing several launch profiles into one runtime', async () => {
  const { client } = setup()
  const [agent] = await client.listAgents({ provider: 'codex-app-server' })
  expect(agent.identity.id).toBe('reviewer')
  expect(agent.runtime.provider).toBe('')
  expect(agent.launches?.map((l) => l.id)).toEqual(['review-code', 'review-docs'])
  expect(agent.runtime.systemPrompt).toBe('System instructions')
  expect(agent.runtime.prompt).toBe('Agent instructions')
  expect(agent.live).toBeUndefined()
})

it('reviews edits and creates locally, and saves without changing the catalog or making a request', async () => {
  const { client, fetcher } = setup()
  const [original] = await client.listAgents()
  const plan = await client.planChange({ agentId: 'reviewer', draft: { ...draftFromAgent(original), summary: 'Changed' } })
  fetcher.mockClear()
  const result = await client.saveDemonstration!(plan.id)
  expect(result.status).toBe('demonstrated')
  expect(result.message).toBe(DEMO_SAVE_MESSAGE)
  expect(result.agent).toBeUndefined()
  expect(fetcher).not.toHaveBeenCalled()
  expect(await client.getAgent('reviewer')).toEqual(original)
  const create = await client.planLifecycle({ op: 'agent.create', agentId: 'new-agent', name: 'New Agent', draft: blankDraft() })
  fetcher.mockClear()
  await client.saveDemonstration!(create.id)
  expect(fetcher).not.toHaveBeenCalled()
  expect(await client.getAgent('new-agent')).toBeNull()
})

it('refuses invalid demonstrations and reports denied reads', async () => {
  const { client } = setup()
  const plan = await client.planLifecycle({ op: 'agent.create', agentId: 'reviewer', name: 'Duplicate', draft: blankDraft() })
  await expect(client.saveDemonstration!(plan.id)).rejects.toThrow('valid draft')
  const denied = createCatalogAgentsClient(async () => new Response('', { status: 403 }))
  await expect(denied.listAgents()).rejects.toThrow('read was denied (403)')
})
