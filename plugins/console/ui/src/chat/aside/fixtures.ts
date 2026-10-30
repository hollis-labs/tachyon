import type { Activity, Bookmark, Plan, Rule, Topic, WorkItem } from './data'

// Golden r2 content for ?demo=cards, so the aside is reviewable on the real route when a thread has none.
const now = Date.now()
const at = (hh: number, mm: number) => { const d = new Date(now); d.setHours(hh, mm, 0, 0); return d.getTime() }

export const DEMO_PLAN: Plan = { id: 'p', status: 'active', revision: 1, task_ref: 'CW-0105', title: 'Generative UI epic', steps: [
  ['Read the options paper', 'done'], ['Compare A2UI and MCP Apps', 'done'], ['List card kinds in Tachyon', 'done'], ['Interview Chrispian on gestures', 'done'], ['Write the options paper', 'done'],
  ['Draft shared UI vocabulary', 'doing'], ['Run the Parallax spike', 'todo'], ['Hand off to the Designer', 'todo'],
].map(([title, status], i) => ({ id: `s${i}`, position: i, title, status: status as 'done' })) }
export const DEMO_TOPICS: Topic[] = [['Generative UI direction', 14, 2], ['Parallax candidate gate', 14, 20], ['Plugin region names', 13, 55], ['Who owns the vocabulary', 13, 40]].map(([title, h, m], i) => ({ id: `t${i}`, title: title as string, message_ref: `m${i}`, seq: i + 1, added_by: 'agent' as const, created_at: at(h as number, m as number) }))
export const DEMO_BOOKMARKS: Bookmark[] = [{ id: 'b1', message_ref: 'm1', note: 'Three viable paths. I recommend the hybrid; it keeps go-envelopes…', created_at: at(14, 2) }, { id: 'b2', message_ref: 'm2', note: 'Cards must be answerable in place.', created_at: at(13, 31) }, { id: 'b3', message_ref: 'm3', note: 'Owner for the vocabulary is the Designer.', created_at: at(12, 48) }]
export const DEMO_WORK: { items: WorkItem[]; source: string } = { source: 'planner', items: [
  { id: 'CW-0105', kind: 'theme', title: 'Generative UI epic', status: 'doing', progress: 0.55, needs_user: true, updated_at: now },
  { id: 'CW-0106', kind: 'theme', title: 'Plugin region vocabulary', status: 'doing', progress: 0.33, updated_at: now },
  { id: 'CW-0107', kind: 'theme', title: 'Parallax candidate gate', status: 'todo', progress: 0, updated_at: now },
  { id: 'CW-0108', kind: 'task', title: 'Draft shared UI vocabulary', status: 'doing', updated_at: now },
  { id: 'DEC-0042', kind: 'decision', title: 'Generative UI direction', status: 'blocked', needs_user: true, updated_at: now },
] }
export const DEMO_ACTIVITY: { activity: Activity[]; has_more: boolean } = { has_more: true, activity: [
  { seq: 12, at: at(14, 2), type: 'card.sent', actor: 'agent:portfolio-manager', cite: [{ kind: 'task', ref: 'DEC-0042', label: 'DEC-…0042' }], payload: { body: 'Waiting on you: pick a path for generative UI.' } },
  { seq: 11, at: at(14, 0), type: 'task.closed', actor: 'agent:task-ops-manager', cite: [{ kind: 'task', ref: 'CW-0104', label: 'CW-…0104' }], payload: { title: 'Wire the registry v2 kinds', summary: '3 files · +120 −18 · tests green', cite: [{ label: 'PR 164' }], review_ref: 'pr/164' } },
  { seq: 10, at: at(14, 3), type: 'plan.changed', actor: 'agent:portfolio-manager', cite: [{ kind: 'task', ref: 'CW-0105', label: 'CW-…0105' }], payload: { summary: '5 of 8 done' } },
  { seq: 9, at: at(14, 1), type: 'tool.call', actor: 'agent:portfolio-manager', payload: { summary: '3 tool calls finished' } },
  { seq: 8, at: at(13, 55), type: 'file.shared', actor: 'user', cite: [{ kind: 'file', ref: 'options-paper.md', label: 'options-paper.md' }] },
  { seq: 7, at: now - 86_400_000 + 1000, type: 'approval.decided', actor: 'user', cite: [{ kind: 'task', ref: 'torque_task_delete', label: 'torque_task_delete' }], payload: { summary: 'Approval denied' } },
  { seq: 6, at: now - 86_400_000, type: 'note', actor: 'agent:task-ops-manager', cite: [{ kind: 'task', ref: 'CW-0104', label: 'CW-…0104' }], payload: { summary: 'accepted' } },
] }
export const DEMO_RULES: Rule[] = [{ id: 'r1', tool: 'torque_task_list', effect: 'allow', created_at: now }]
export const DEMO_DOC = { name: 'options-paper.md', shared: true, text: '### Generative UI direction\n\nKeep `go-envelopes` as the outer contract and add one envelope type carrying an A2UI-compatible subset pinned to a version. Use MCP Apps for sandboxed third-party UI.\n\n### Open questions\n\nGranularity, Sigil\'s boundary, who owns the vocabulary, authority for PRs, where to start on style standards.' }
