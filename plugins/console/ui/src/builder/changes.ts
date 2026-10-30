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
export const OPEN_STATES: readonly ChangeState[] = ['draft', 'awaiting_handoff', 'awaiting_approval', 'approved', 'applying', 'stale']
export const isOpen = (s: ChangeState) => OPEN_STATES.includes(s)
export const isWaiting = (s: ChangeState) => s === 'awaiting_handoff' || s === 'awaiting_approval' || s === 'approved' || s === 'applying'

export function minutesAndSeconds(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

export const newRequestId = () => `builder-${crypto.randomUUID()}`

/** '12m ago' / 'Yesterday' / a date; the caller keeps the absolute time in a title. */
export function relativeTime(at: number, now = Date.now()): string {
  const ms = at > 1e12 ? at : at * 1000
  const s = Math.max(0, Math.floor((now - ms) / 1000))
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  if (s < 172800) return 'Yesterday'
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d ago`
  return new Date(ms).toLocaleDateString()
}

// ---- Library parts (prompts and policies): internal/builderchange/partops.go
export type PartKind = 'prompts' | 'policies'
export interface PartField {
  path: string
  label: string
  kind: 'text' | 'longtext' | 'chips'
}

export const PART_FIELDS: Record<PartKind, readonly PartField[]> = {
  prompts: [
    { path: 'name', label: 'Name', kind: 'text' },
    { path: 'summary', label: 'Summary', kind: 'text' },
    { path: 'body', label: 'Body', kind: 'longtext' },
  ],
  policies: [
    { path: 'name', label: 'Name', kind: 'text' },
    { path: 'summary', label: 'Summary', kind: 'text' },
    { path: 'permission_mode', label: 'Permission mode', kind: 'text' },
    { path: 'network', label: 'Network', kind: 'text' },
    { path: 'default_sandbox', label: 'Sandbox', kind: 'text' },
    { path: 'mcp_scopes', label: 'Scopes', kind: 'chips' },
  ],
}

type PartItem = { id: string; revision?: unknown; bundle?: unknown; [k: string]: unknown }

/** What the part has now for a field; policy fields live in the bundle when it sets them. */
export function partRead(it: PartItem, field: PartField): unknown {
  const bundle = typeof it.bundle === 'object' && it.bundle !== null ? (it.bundle as Record<string, unknown>) : {}
  const v = field.path in bundle ? bundle[field.path] : it[field.path]
  if (field.kind === 'chips') return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  return typeof v === 'string' ? v : ''
}

export function changedPartPaths(kind: PartKind, it: PartItem, draft: Draft): string[] {
  return PART_FIELDS[kind].filter((f) => f.path in draft && !same(draft[f.path], partRead(it, f))).map((f) => f.path)
}

/** One set op per changed field; the part revision is the expected_sha. */
export function buildPartOps(kind: PartKind, it: PartItem, draft: Draft): ChangeOp[] {
  return changedPartPaths(kind, it, draft).map((path) => ({
    target: { kind: 'part', id: it.id, part_kind: kind === 'prompts' ? 'prompt' : 'policy' },
    expected_sha: String(it.revision ?? ''),
    op: { type: 'set', path, value: draft[path] },
  }))
}

/** Dirty-bar text shared by every edit surface: 'Changing 2 fields: body, summary'. */
export function changingText(labels: (string | undefined)[]): string {
  if (labels.length === 0) return 'No unsaved changes'
  return `Changing ${labels.length} field${labels.length === 1 ? '' : 's'}: ${labels.join(', ')}`
}

