import { hostFetch } from '@tachyon/host-sdk'
import type { AgentState, PolicyTag } from './vocab'

// Builder + Registry read model (internal/builderapi/openapi.yaml). GETs are open
// but prompt bodies may be operator-only: a 401/403 there is tolerated, not an error screen.
export const BUILDER_BASE = '/api/plugins/console/v1/builder'

export interface Summary {
  agents?: { total: number; active: number; disabled: number; retired?: number | null }
  launches?: { total: number; live?: number | null }
  library?: { prompts: number; skills: number; tool_servers?: number | null; policies: number }
  projects?: { total: number }
  open_changes?: number
  catalog?: { observed_at?: number; source_status?: 'ok' | 'stale' | 'unreachable' }
  capabilities?: { retired?: boolean; one_step_save?: boolean; resolved_by?: string }
}

export interface AgentRow {
  id: string
  name: string
  urn?: string | null
  provider: string
  model?: string | null
  enabled: boolean
  state: AgentState
  policy_tag?: PolicyTag | null
  launch_count: number
  layer?: string | null
  sha?: string | null
  owned_draft: boolean
}
export interface AgentList {
  agents: AgentRow[]
  total: number
  next_cursor?: string | null
}

export interface AgentDetail {
  id: string
  state: AgentState
  editable: boolean
  owned_draft: boolean
  identity: {
    id: string
    name: string
    description?: string
    urn?: string | null
    roles?: string[]
  }
  runtime: {
    provider: string
    model?: string | null
    extends?: string | null
    abstract?: boolean
    enabled?: boolean
  }
  prompt?: {
    parts?: {
      kind: string
      id: string
      rev: number
      label: string
      bytes: number
      tokens_est: number
    }[]
    prompts?: string[]
    boot_fragments?: string[]
  }
  tools_skills?: { mcp_servers?: string[] | null; skills?: string[] }
  policy?: {
    permission_mode?: string | null
    network?: string | null
    default_sandbox?: string | null
    mcp_scopes?: string[]
  }
  launches?: { id: string; project: string; worker: string; enabled: boolean; live_count: number }[]
  revision?: {
    sha?: string | null
    snapshot_seq?: number | null
    layer?: string | null
    path?: string | null
    observed_at?: number | null
  }
}

export interface Resolved {
  resolved_by: 'tether' | 'tachyon_approx'
  tool_servers?: { id: string; status: string; tool_count?: number | null }[]
  skills?: { id: string; name: string }[]
  policy?: { permission_mode?: string | null; network?: string | null; default_sandbox?: string | null }
  scopes?: string[]
  prompt_parts?: { label: string; bytes: number; tokens_est: number }[]
  prompt_tokens_est?: number
  prompt_tokens_estimated?: boolean
  extends_chain?: string[]
  notes?: string[]
}

export interface UsedBy {
  live_sessions?: { session_id: string; launch_id: string; state: string }[]
  launches?: { id: string; project: string; enabled: boolean }[]
  teams?: { id: string; name: string; launch_id?: string | null }[]
  extended_by?: string[]
}

export interface Checks {
  checks: Check[]
  summary?: { ok: number; warn: number; fail: number }
}

export interface HistoryEntry {
  at: number
  plan_id: string
  name: string
  actor: string
  files?: string[]
  privileged?: string[]
  activation?: string
  diff?: string | null
}

export interface History {
  entries: HistoryEntry[]
  next_cursor?: string | null
}

export interface Launch {
  id: string
  agent: string
  provider: string
  model?: string | null
  project: string
  worker: string
  env?: { keys: string[]; secret_like_count: number }
  route?: string | null
  chat?: boolean
  enabled: boolean
  live_count: number
}

export type LibraryKind = 'prompts' | 'skills' | 'tool_servers' | 'policies'

export interface LibraryItem {
  kind: LibraryKind
  id: string
  name: string
  summary?: string
  used_by_count?: number
  editable?: boolean
  [extra: string]: unknown
}

export type ChangeState = 'draft' | 'awaiting_handoff' | 'awaiting_approval' | 'approved' | 'applying' | 'applied' | 'failed' | 'rolled_back' | 'stale' | 'expired' | 'cancelled'

export interface ChangeOp {
  target: {
    kind: 'agent' | 'launch' | 'project' | 'skill' | 'part'
    id: string
    part_kind?: 'prompt' | 'policy'
  }
  expected_sha?: string
  op: {
    type: 'set' | 'append' | 'remove' | 'create' | 'restore_version' | 'archive'
    path?: string
    value?: unknown
  }
}

export interface Check {
  name: string
  status: 'ok' | 'warn' | 'fail'
  message: string
  remedy?: string | null
  entity?: string
}

export interface Change {
  change_id: string
  request_id: string
  state: ChangeState
  note?: string
  diff?: string
  checks?: Check[]
  privileged?: { path: string; field: string }[]
  ack_privileged?: boolean
  effect?: {
    running_sessions?: { session_id: string; agent_id: string; launch_id: string }[]
    activation?: string
    restart_required?: string[]
  }
  plan?: {
    plan_id: string
    state: string
    digest: string
    expires_at: number
    approve_command: string
  } | null
  result?: {
    activation?: string
    restart_required?: string[]
    reloaded?: boolean
    reload_error?: string | null
    applied_at?: number | null
    detected?: boolean
  } | null
  failure?: { step: string; code: string; message: string } | null
  /** Handoff mode: the bundle is ready and the operator plans it in Tether. Commands come from the backend, verbatim. */
  handoff?: {
    mode: string
    name: string
    bundle_sha256: string
    files: number
    session_available: boolean
    reason?: string
    commands?: { step: 'plan' | 'show' | 'approve' | 'apply' | string; label: string; kind?: 'shell' | 'action' | string; command?: string; tool?: string; instruction?: string }[]
  } | null
  stale?: {
    changed_paths: string[]
    theirs_sha: string
    mine_diff: string
    theirs_diff: string
  } | null
}

export interface Capabilities {
  changes: { mode: 'direct' | 'handoff' | 'unavailable'; handoff: string[]; applier_agent?: string }
}
export interface Bundle {
  name: string
  note?: string
  files: { path: string; content: string }[]
  sha256: string
}

export class BuilderError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await hostFetch(BUILDER_BASE + path, { headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let code = 'error'
    let message = `Request failed (${res.status})`
    try {
      const b = (await res.json()) as { code?: string; message?: string }
      code = b.code ?? code
      message = b.message ?? message
    } catch {
      // keep the status text
    }
    throw new BuilderError(res.status, code, message)
  }
  return (await res.json()) as T
}

async function send<T>(path: string, body: unknown): Promise<T> {
  const res = await hostFetch(BUILDER_BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    let code = 'error'
    let message = `Request failed (${res.status})`
    try {
      const b = (await res.json()) as { code?: string; message?: string }
      code = b.code ?? code
      message = b.message ?? message
    } catch {
      // keep the status text
    }
    throw new BuilderError(res.status, code, message)
  }
  return (await res.json()) as T
}

export interface AgentQuery {
  q?: string
  provider?: string
  state?: AgentState
  cursor?: string
}

export const builderApi = {
  summary: () => get<Summary>('/summary'),
  agents: (query: AgentQuery = {}) => {
    const p = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) if (v) p.set(k, v)
    const qs = p.toString()
    return get<AgentList>(`/agents${qs ? `?${qs}` : ''}`)
  },
  agent: (id: string) => get<AgentDetail>(`/agents/${encodeURIComponent(id)}`),
  resolved: (id: string) => get<Resolved>(`/agents/${encodeURIComponent(id)}/resolved`),
  usedBy: (id: string) => get<UsedBy>(`/agents/${encodeURIComponent(id)}/used_by`),
  checks: (id: string) => get<Checks>(`/agents/${encodeURIComponent(id)}/checks`),
  history: (id: string, cursor?: string) => get<History>(`/agents/${encodeURIComponent(id)}/history${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`),
  launches: (query: { q?: string; live?: boolean } = {}) => {
    const p = new URLSearchParams()
    if (query.q) p.set('q', query.q)
    if (query.live) p.set('live', '1')
    const qs = p.toString()
    return get<{ launches: Launch[]; next_cursor?: string | null }>(`/launches${qs ? `?${qs}` : ''}`)
  },
  library: (kind: LibraryKind, q = '') => get<{ items: LibraryItem[] }>(`/library/${kind}${q ? `?q=${encodeURIComponent(q)}` : ''}`),
  libraryItem: (kind: LibraryKind, id: string) => get<LibraryItem>(`/library/${kind}/${encodeURIComponent(id)}`),
  change: (id: string) => get<Change>(`/changes/${encodeURIComponent(id)}`),
  createChange: (request: { request_id: string; note?: string; ops: ChangeOp[] }) => send<Change>('/changes', request),
  submit: (id: string, ackPrivileged: boolean) => send<Change>(`/changes/${encodeURIComponent(id)}/submit`, { ack_privileged: ackPrivileged }),
  cancel: (id: string) => send<Change>(`/changes/${encodeURIComponent(id)}/cancel`, {}),
  rearm: (id: string) => send<Change>(`/changes/${encodeURIComponent(id)}/rearm`, {}),
  capabilities: () => get<Capabilities>('/capabilities'),
  /** The bundle holds full file content: fetch it on demand for copy or download, never keep or log it. */
  bundle: (id: string) => get<Bundle>(`/changes/${encodeURIComponent(id)}/bundle`),
  check: (id: string) => send<Change>(`/changes/${encodeURIComponent(id)}/check`, {}),
}

export function errorMessage(e: unknown): string {
  if (e instanceof BuilderError && (e.status === 401 || e.status === 403)) return 'Operator access is needed to view this.'
  if (e instanceof BuilderError && e.code === 'change_open') return 'Another change to this agent is already open. Finish or cancel it first.'
  if (e instanceof BuilderError && e.code === 'rate_limited') return 'Too many changes in a minute. Wait a moment and try again.'
  if (e instanceof BuilderError && e.code === 'ack_required') return 'Confirm the privileged fields before submitting.'
  return e instanceof Error ? e.message : 'Could not load.'
}
