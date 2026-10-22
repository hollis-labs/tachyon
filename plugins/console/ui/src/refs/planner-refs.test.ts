import { describe, expect, it } from 'vitest'
import { buildPreviewCard, createDefaultRefRegistry, detect } from '.'
import { createPlannerResolver, plannerEntity } from './planner-item'
import { createForgeKinds } from '../forge/refs/kinds'

const reg = createDefaultRefRegistry()
for (const k of createForgeKinds()) reg.register(k, { replace: true })
const refs = (text: string) => detect(text, reg).flatMap((s) => (s.type === 'text' ? [] : [`${s.kind}:${s.canonical}`]))

describe('planner ids', () => {
  it('links themes, decisions, risks, inbox and workstreams', () => {
    expect(refs('PR-07 and DEC-052, RK-12, IN-88 plus WS-tangent-consolidation')).toEqual([
      'planner.theme:PR-07', 'planner.decision:DEC-052', 'planner.risk:RK-12', 'planner.inbox:IN-88', 'planner.workstream:WS-tangent-consolidation',
    ])
  })
  it('leaves pull-request numbers and code alone', () => {
    expect(refs('see PR-113 and `PR-07` and DEC-052.md and a-PR-07')).toEqual([])
  })
})

describe('planner resolver', () => {
  it('turns a decision into a card with a lead paragraph and key fields', () => {
    const e = plannerEntity({
      id: 'u', rev: 1, title: 'Done means merged', status: 'accepted', description: 'Teams treated complete as release-ready.',
      rationale: 'Nothing is released.', updated: '2026-10-20T00:00:00Z', comments: [{ author: 'a', text: 'x' }] as never,
    })
    expect(e).toMatchObject({ title: 'Done means merged', status: { label: 'accepted', tone: 'success' }, updatedAt: '2026-10-20T00:00:00Z' })
    expect(e.description).toBe('Teams treated complete as release-ready.\n\nWhy: Nothing is released.')
    expect(e.fields).toContainEqual({ label: 'Comments', value: '1' })
  })
  it('maps unknown and ambiguous ids to not-found and rethrows real failures', async () => {
    const notFound = Object.assign(new Error('nf'), { status: 404 })
    const resolver = createPlannerResolver(async (ref) => {
      if (ref === 'PR-99') throw notFound
      if (ref === 'PR-98') throw Object.assign(new Error('conflict'), { status: 409 })
      if (ref === 'PR-97') throw new Error('network')
      return { item: { id: 'u', rev: 1, title: 'Hub', status: 'proposed' } }
    })
    const ctx = { signal: new AbortController().signal }
    const ok = await resolver.resolveMany(['PR-01', 'PR-99', 'PR-98'], ctx)
    expect(ok['PR-01']).toMatchObject({ status: 'found', entity: { title: 'Hub' } })
    expect(ok['PR-99']).toEqual({ status: 'not-found' })
    expect(ok['PR-98']).toEqual({ status: 'not-found' })
    await expect(resolver.resolveMany(['PR-97'], ctx)).rejects.toThrow('network')
  })
})

describe('pull requests', () => {
  it('reads org repo names written with a number, and nothing else', () => {
    expect(refs('tether#490 and tachyon#113, but issue#12 and C# and nil#')).toEqual(['forge.pr:github:hollis-labs/tether#490', 'forge.pr:github:hollis-labs/tachyon#113'])
    expect(refs('hollis-labs/tachyon#99')).toEqual(['forge.pr:github:hollis-labs/tachyon#99'])
  })
  it('opens a pull request in our workspace, not on GitHub', () => {
    const ref = { kind: 'forge.pr', canonical: 'github:hollis-labs/tachyon#115' }
    const card = buildPreviewCard(reg, ref, { status: 'unsupported' })
    expect(card.open).toEqual({ type: 'route', to: '/pr/github/hollis-labs/tachyon/115' })
    expect(card.openLabel).toBe('Open workspace')
    const url = detect('https://github.com/hollis-labs/tachyon/pull/115', reg).flatMap((s) => (s.type === 'text' ? [] : [s.canonical]))
    expect(url).toEqual(['github:hollis-labs/tachyon#115'])
  })
})
