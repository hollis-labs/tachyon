import type { ConfirmResult } from '@console/seams/identity-confirm'
import type { AgentsClient } from './client'
import type { AgentTemplate, AssignmentRecord, LifecyclePlanRequest, RetireImpact, TeamRecord } from './lifecycle'
import type { AgentPlan, ApplyOutcome, AuditEntry, PlanRequest } from './plan'
import type { AgentListQuery, AgentRecord } from './types'

/** Console plugin backend base (fronts Tether + Torque; see BACKEND_NEEDS.md). */
export const AGENTS_API_BASE = '/api/plugins/console/v1'

export class AgentsHttpError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'AgentsHttpError'
  }
}

type Fetch = typeof fetch

const enc = encodeURIComponent

function qs(params: Record<string, string | undefined>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') p.set(k, v)
  const s = p.toString()
  return s ? `?${s}` : ''
}

/**
 * The real AgentsClient: a thin HTTP adapter over the contract in
 * BACKEND_NEEDS.md / docs/data-gaps.md (C2). The server owns plan identity,
 * diff/risk recomputation, step-up verification and audit; this client only
 * transports. Until the backend exists every call rejects, and the views show
 * their error/empty states.
 */
export function createHttpAgentsClient(base: string = AGENTS_API_BASE, fetchImpl?: Fetch): AgentsClient {
  const doFetch: Fetch = (...args) => (fetchImpl ?? fetch)(...args)

  async function request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
    const res = await doFetch(`${base}${path}`, {
      method,
      headers: body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    if (!res.ok) {
      let message = `${method} ${path} failed (${res.status}${res.statusText ? ` ${res.statusText}` : ''})`
      try {
        const err = JSON.parse(text) as { error?: string; message?: string }
        if (err?.error || err?.message) message = String(err.error ?? err.message)
      } catch {
        /* non-JSON error body */
      }
      throw new AgentsHttpError(res.status, message)
    }
    if (!text) return undefined as T
    try {
      return JSON.parse(text) as T
    } catch {
      throw new AgentsHttpError(res.status, `${method} ${path} returned a non-JSON response (HTTP ${res.status}); is the endpoint implemented?`)
    }
  }

  /** GET that maps 404 to null. */
  async function getOrNull<T>(path: string): Promise<T | null> {
    try {
      return await request<T>('GET', path)
    } catch (err) {
      if (err instanceof AgentsHttpError && err.status === 404) return null
      throw err
    }
  }

  const confirmBody = (confirmation: ConfirmResult) => ({ confirmation })

  return {
    listAgents: (query?: AgentListQuery) => request<AgentRecord[]>('GET', `/agents${qs({ search: query?.search, provider: query?.provider, role: query?.role })}`),
    getAgent: (id) => getOrNull<AgentRecord>(`/agents/${enc(id)}`),

    planChange: (req: PlanRequest) => request<AgentPlan>('POST', `/agents/${enc(req.agentId)}/plans`, { draft: req.draft, baseRevision: req.baseRevision, note: req.note }),
    getPlan: (planId) => getOrNull<AgentPlan>(`/agent-plans/${enc(planId)}`),
    approvePlan: (planId, confirmation) => request<AgentPlan>('POST', `/agent-plans/${enc(planId)}/approve`, confirmBody(confirmation)),
    applyPlan: (planId, confirmation) => request<ApplyOutcome>('POST', `/agent-plans/${enc(planId)}/apply`, confirmBody(confirmation)),
    rollbackApply: (planId, confirmation) => request<ApplyOutcome>('POST', `/agent-plans/${enc(planId)}/rollback`, confirmBody(confirmation)),
    listAudit: (agentId) => request<AuditEntry[]>('GET', `/agents/${enc(agentId)}/audit`),

    planLifecycle: (req: LifecyclePlanRequest) => request<AgentPlan>('POST', '/agent-plans/lifecycle', req),
    getRetireImpact: (agentId) => request<RetireImpact>('GET', `/agents/${enc(agentId)}/retire-impact`),
    listTemplates: () => request<AgentTemplate[]>('GET', '/agent-templates'),
    listTeams: () => request<TeamRecord[]>('GET', '/teams'),
    getTeam: (id) => getOrNull<TeamRecord>(`/teams/${enc(id)}`),
    listAssignments: (query) => request<AssignmentRecord[]>('GET', `/assignments${qs({ agent_id: query?.agentId, target_id: query?.targetId })}`),
  }
}
