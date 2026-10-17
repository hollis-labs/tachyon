import { hostFetch } from "@tachyon/host-sdk"
import type { AgentsClient } from './client'
import { filterAgents } from './filter'
import { computeLifecyclePlan } from './lifecycle'
import { computePlan, type AgentPlan } from './plan'
import type { AgentRecord } from './types'

interface CatalogAgent {
  id: string
  name?: string
  urn?: string
  enabled?: boolean
  roles?: string[] | null
  description?: string
  system_prompt?: string
  agent_prompt?: string
  skills?: string[] | null
  mcp_scopes?: string[] | null
  mcp?: { servers?: string[] | null }
  permissions?: { permission_mode?: string; default_sandbox?: string; network?: boolean }
}

export interface CatalogLaunch {
  id: string
  agent: string
  provider?: string
  project?: string
  enabled?: boolean
}

/** Agent fields remain agent fields; launch configuration is shown separately. */
export function catalogAgent(a: CatalogAgent, launches: CatalogLaunch[]): AgentRecord {
  return {
    identity: { id: a.id, name: a.name || a.id, urn: a.urn, enabled: a.enabled !== false, roles: a.roles ?? [], summary: a.description },
    runtime: { provider: '', systemPrompt: a.system_prompt, prompt: a.agent_prompt },
    policy: { permissionMode: a.permissions?.permission_mode ?? 'default', sandbox: a.permissions?.default_sandbox, network: a.permissions?.network, scopes: a.mcp_scopes ?? [], rules: [] },
    tools: a.mcp?.servers ?? [],
    skills: a.skills ?? [],
    teams: [],
    source: { path: `Tether catalog · ${a.id}` },
    launches: launches.filter((l) => l.agent === a.id),
  }
}

export const DEMO_SAVE_MESSAGE = 'Demonstration save succeeded (no-op). Nothing was written to Tether or Torque. Write access is pending.'

/** Live reads, local review, and an explicitly non-persistent save demonstration. */
export function createCatalogAgentsClient(fetchImpl: typeof fetch = hostFetch): AgentsClient {
  const plans = new Map<string, AgentPlan>()
  const unavailable = async (): Promise<never> => { throw new Error('This read is not connected yet. No catalog changes have been made.') }
  const writesUnavailable = async (): Promise<never> => { throw new Error('Write access is pending. Use Save demonstration; no approval or write request is sent.') }
  async function read<T>(kind: string): Promise<T[]> {
    const res = await fetchImpl(`/api/plugins/console/v1/catalog/${kind}`, { headers: { Accept: 'application/json' }, cache: 'no-store' })
    if (!res.ok) {
      throw new Error(res.status === 401 || res.status === 403
        ? `Tether ${kind} read was denied (${res.status}). Check the host's Tether credential and read access.`
        : `Tether ${kind} read failed (${res.status}). Check TETHER_ADDR and TETHER_TOKEN_FILE on the Tachyon host.`)
    }
    const body = await res.json()
    if (!Array.isArray(body[kind])) throw new Error(`Tether returned an invalid ${kind} catalog.`)
    return body[kind]
  }
  async function agents() {
    const [rows, launches] = await Promise.all([read<CatalogAgent>('agents'), read<CatalogLaunch>('launches')])
    return rows.map((a) => catalogAgent(a, launches))
  }
  function remember(plan: AgentPlan) {
    const demo = { ...plan, demonstration: true }
    // Plans only belong to this browser session; bound retained review drafts.
    if (plans.size >= 50) plans.delete(plans.keys().next().value!)
    plans.set(plan.id, structuredClone(demo))
    return demo
  }
  const meta = () => ({ id: `demo-${crypto.randomUUID()}`, now: Date.now(), actor: { id: 'local-preview', label: 'Local demonstration' } })
  return {
    writeMode: 'demonstration',
    listAgents: async (query) => filterAgents(await agents(), query),
    getAgent: async (id) => (await agents()).find((a) => a.identity.id === id) ?? null,
    planChange: async (req) => {
      const agent = (await agents()).find((a) => a.identity.id === req.agentId)
      if (!agent) throw new Error('Agent is no longer in the catalog. Reload before reviewing changes.')
      return remember(computePlan(agent, req, meta()))
    },
    planLifecycle: async (req) => {
      if (req.op !== 'agent.create') return unavailable()
      return remember(computeLifecyclePlan(req, { agents: await agents(), teams: [], assignments: [] }, meta()))
    },
    getPlan: async (id) => structuredClone(plans.get(id) ?? null),
    saveDemonstration: async (id) => {
      const plan = plans.get(id)
      if (!plan || plan.status !== 'proposed' || plan.issues.some((i) => i.level === 'error')) throw new Error('Review a valid draft before saving the demonstration.')
      const demonstrated: AgentPlan = { ...plan, status: 'demonstrated' }
      plans.set(id, demonstrated)
      return { status: 'demonstrated', plan: structuredClone(demonstrated), message: DEMO_SAVE_MESSAGE }
    },
    approvePlan: writesUnavailable,
    applyPlan: writesUnavailable,
    rollbackApply: writesUnavailable,
    listTemplates: async () => [],
    listTeams: unavailable,
    getTeam: unavailable,
    listAssignments: unavailable,
    listAudit: unavailable,
    getRetireImpact: unavailable,
  }
}
