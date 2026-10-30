import type { AgentDetail, ChangeOp, ChangeState } from './api'

// Editable agent fields, by editor-model path (internal/builderchange/patch.go).
export type FieldKind = 'text' | 'longtext' | 'bool' | 'chips'
export interface FieldDef {
  path: string
  label: string
  kind: FieldKind
  read: (a: AgentDetail) => unknown
}

const list = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])

export const FIELDS: readonly FieldDef[] = [
  {
    path: 'identity.description',
    label: 'Description',
    kind: 'longtext',
    read: (a) => a.identity.description ?? '',
  },
  { path: 'identity.roles', label: 'Roles', kind: 'chips', read: (a) => list(a.identity.roles) },
  { path: 'runtime.model', label: 'Model', kind: 'text', read: (a) => a.runtime.model ?? '' },
  {
    path: 'runtime.enabled',
    label: 'Enabled',
    kind: 'bool',
    read: (a) => a.runtime.enabled !== false,
  },
  {
    path: 'tools_skills.skills',
    label: 'Skills',
    kind: 'chips',
    read: (a) => list(a.tools_skills?.skills),
  },
  {
    path: 'policy.permission_mode',
    label: 'Permission mode',
    kind: 'text',
    read: (a) => a.policy?.permission_mode ?? '',
  },
  { path: 'policy.network', label: 'Network', kind: 'text', read: (a) => a.policy?.network ?? '' },
  {
    path: 'policy.default_sandbox',
    label: 'Sandbox',
    kind: 'text',
    read: (a) => a.policy?.default_sandbox ?? '',
  },
  {
    path: 'policy.mcp_scopes',
    label: 'Scopes',
    kind: 'chips',
    read: (a) => list(a.policy?.mcp_scopes),
  },
]

export const fieldByPath = (path: string) => FIELDS.find((f) => f.path === path)

export type Draft = Readonly<Record<string, unknown>>

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

/** Paths whose draft value differs from what the agent has now. */
export function changedPaths(agent: AgentDetail, draft: Draft): string[] {
  return FIELDS.filter((f) => f.path in draft && !same(draft[f.path], f.read(agent))).map((f) => f.path)
}

/** The edit as change ops against the read model's sha. Never sends an unchanged field. */
export function buildOps(agent: AgentDetail, draft: Draft): ChangeOp[] {
  return changedPaths(agent, draft).map((path) => ({
    target: { kind: 'agent', id: agent.id },
    expected_sha: agent.revision?.sha ?? '',
    op: { type: 'set', path, value: draft[path] },
  }))
}

/** States in which the change still holds its paths. */
export const OPEN_STATES: readonly ChangeState[] = ['draft', 'awaiting_approval', 'approved', 'applying', 'stale']
export const isOpen = (s: ChangeState) => OPEN_STATES.includes(s)
export const isWaiting = (s: ChangeState) => s === 'awaiting_approval' || s === 'approved' || s === 'applying'

export function minutesAndSeconds(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

export const newRequestId = () => `builder-${crypto.randomUUID()}`
