import { describe, expect, it } from 'vitest'
import { createDefaultRefRegistry, detect } from '.'
import { agentEntity, createTetherAgentResolver, createTetherPlanResolver, planEntity } from './tether-object'

const reg = createDefaultRefRegistry()
const refs = (text: string) => detect(text, reg).flatMap((s) => (s.type === 'text' ? [] : [`${s.kind}:${s.canonical}`]))
const ctx = { signal: new AbortController().signal }
const reply = (status: number, body: unknown = {}) => async () => new Response(JSON.stringify(body), { status })

describe('tether object ids', () => {
  it('links plans and agents by id', () => {
    expect(refs('plan cp_928c2c714c131ae1c23ff24b for agt_abcde12345')).toEqual(['tether.plan:cp_928c2c714c131ae1c23ff24b', 'tether.agent:agt_abcde12345'])
  })
  it('ignores near misses', () => {
    expect(refs('cp_928c2c714c131ae1c23ff24 xcp_928c2c714c131ae1c23ff24b agt_ABCDE12345 agt_abcde123456')).toEqual([])
  })
})

describe('tether object cards', () => {
  it('summarises a plan with its files and state', () => {
    const e = planEntity({ plan_id: 'cp_1', name: 'Add agent', state: 'applied', validation_ok: true, file_count: 3, files: [{ path: 'a.yaml', action: 'create' }] })
    expect(e).toMatchObject({ title: 'Add agent', status: { label: 'applied', tone: 'success' } })
    expect(e.description).toBe('create a.yaml\n… and 2 more')
    expect(e.fields).toContainEqual({ label: 'Valid', value: 'yes' })
  })
  it('summarises an agent', () => {
    const e = agentEntity({ urn: 'msg://agent/x/agt_1', display_name: 'Reviewer', role: 'review', status: 'active', tags: ['a', 'b'] })
    expect(e).toMatchObject({ title: 'Reviewer', status: { label: 'active', tone: 'success' } })
    expect(e.fields).toContainEqual({ label: 'Tags', value: 'a, b' })
  })
})

describe('tether object resolvers', () => {
  it('maps 404 to not-found and refusals to a retryable error', async () => {
    expect(await createTetherPlanResolver(reply(404)).resolveMany(['cp_x'], ctx)).toEqual({ cp_x: { status: 'not-found' } })
    await expect(createTetherAgentResolver(reply(403)).resolveMany(['agt_x'], ctx)).rejects.toThrow(/Operator access/)
    await expect(createTetherAgentResolver(reply(502)).resolveMany(['agt_x'], ctx)).rejects.toThrow(/502/)
  })
  it('reads the projection route for the id', async () => {
    let seen = ''
    const out = await createTetherPlanResolver(async (p) => { seen = p; return new Response(JSON.stringify({ plan_id: 'cp_1', name: 'N', state: 'planned' })) }).resolveMany(['cp_1'], ctx)
    expect(seen).toBe('/api/plugins/console/v1/tether/plans/cp_1')
    expect(out.cp_1).toMatchObject({ status: 'found' })
  })
})
