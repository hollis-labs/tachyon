import type { BuilderKind, ObjectRecord } from './owned-builder-client'

export type BuilderDraft = Record<string, string | boolean>
const text = (value: unknown) => typeof value === 'string' ? value : ''
const list = (value: unknown) => Array.isArray(value) ? value.join(', ') : ''
const object = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {}
const values = (value: string | boolean) => [...new Set(String(value).split(',').map(v => v.trim()).filter(Boolean))]

export function builderDraft(kind: BuilderKind, row?: ObjectRecord): BuilderDraft {
  const d = row?.definition ?? {}
  const common = { name: text(d.name), description: text(d.description) }
  if (kind === 'agent') return { ...common, runtime: text(d.runtime), model: text(d.model), enabled: d.enabled === true,
    roles: list(d.roles), instructions: text(d.instructions), tools: list(d.tools), skills: list(d.skills), project_id: text(d.project_id), machine_id: text(d.machine_id) }
  const ext = object(d.external_target), target = object(d.target)
  return { ...common, assignee_kind: d.team_id ? 'team' : 'agent', assignee_id: text(d.team_id || d.agent_id),
    target_mode: d.external_target ? 'external' : 'local', target_kind: text(target.kind) || 'task', target_id: text(target.id),
    provider: text(ext.provider), instance: text(ext.instance), collection: text(ext.collection), external_id: text(ext.external_id),
    role: text(d.role), state: text(d.state) || 'draft' }
}
export function builderDefinition(kind: BuilderKind, draft: BuilderDraft): Record<string, unknown> {
  const common = { name: String(draft.name).trim(), description: String(draft.description) }
  if (kind === 'agent') return { ...common, runtime: String(draft.runtime).trim(), model: String(draft.model).trim(), enabled: draft.enabled,
    roles: values(draft.roles), instructions: draft.instructions, tools: values(draft.tools), skills: values(draft.skills),
    project_id: draft.project_id || null, machine_id: draft.machine_id || null }
  return { ...common, agent_id: draft.assignee_kind === 'agent' ? draft.assignee_id : null, team_id: draft.assignee_kind === 'team' ? draft.assignee_id : null,
    role: draft.role, state: draft.state,
    target: draft.target_mode === 'local' ? { kind: draft.target_kind, id: draft.target_id } : null,
    external_target: draft.target_mode === 'external' ? { provider: String(draft.provider).trim(), instance: String(draft.instance).trim(), collection: String(draft.collection).trim(), external_id: String(draft.external_id).trim() } : null }
}
/** Only authored field deltas are sent; unknown fields/properties remain intact. */
export function builderPatch(kind: BuilderKind, draft: BuilderDraft, base?: ObjectRecord) {
  const next = builderDefinition(kind, draft)
  if (!base) return Object.fromEntries(Object.entries(next).filter(([, value]) => value !== null))
  const before = builderDefinition(kind, builderDraft(kind, base))
  return Object.fromEntries(Object.entries(next).filter(([k, value]) => JSON.stringify(value) !== JSON.stringify(before[k])))
}
export function builderIssues(kind: BuilderKind, draft: BuilderDraft): string[] {
  const issues: string[] = []
  if (!String(draft.name).trim()) issues.push('Give the record a name.')
  if (kind === 'assignment') {
    if (!draft.assignee_id) issues.push('Choose an agent or team.')
    if (draft.target_mode === 'local' && !draft.target_id) issues.push('Choose the work to assign.')
    if (draft.target_mode === 'external' && ['provider', 'instance', 'collection', 'external_id'].some(key => !String(draft[key]).trim())) issues.push('Complete all four external reference fields.')
  }
  return issues
}
