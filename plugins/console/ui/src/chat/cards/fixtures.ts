import type { StreamBlock } from './types'

/** The golden r2 stream, used while a thread has no live cards (?demo=cards) so the design can be reviewed in the real route. */
export function demoBlocks(now: number): StreamBlock[] {
  const t = (m: number) => now + m
  return [
    { id: 'demo-decision', kind: 'decision', at: t(1), title: 'Generative UI direction', cite: 'DEC-…0042', reversible: true, options: [
      { id: 'a', label: 'Hybrid', recommended: true, description: 'Envelopes plus a pinned A2UI subset; MCP Apps for sandboxed third-party UI.' },
      { id: 'b', label: 'A2UI only', description: 'One spec, but we depend on its pace.' },
      { id: 'c', label: 'MCP Apps only', description: 'Sandboxed everywhere; heavier.' },
    ] },
    { id: 'demo-tools', kind: 'tools', at: t(2), tools: ['tesseract_recall', 'torque_task_list', 'caster get'], state: 'done' },
    { id: 'demo-plan', kind: 'plan', at: t(3), title: 'Plan', total: 8, done: 5, doing: 1, steps: [
      { id: '1', title: 'Read the options paper', state: 'done' },
      { id: '2', title: 'Draft shared UI vocabulary', state: 'doing' },
      { id: '3', title: 'Run the Parallax spike', state: 'todo' },
    ] },
    { id: 'demo-approval', kind: 'approval', at: t(4), title: 'Create 3 tasks in project CW', tool: 'torque_task_create', count: 3, canAlways: true, reversible: true },
    { id: 'demo-dispatch', kind: 'dispatch', at: t(5), target: 'Task Ops Manager', effect: 'creates a Torque task', fields: { title: 'Draft shared UI vocabulary', project: 'CW', priority: 'P1' } },
    { id: 'demo-run-done', kind: 'run', at: t(6), run: { runId: 'r1', machine: 'dev-box', requestedBy: 'Portfolio Manager', approval: 'approved', command: 'pnpm lint --filter chat', state: 'finished', exitCode: 0, seconds: 2.1, output: '> chat@0.4.0 lint\n✓ no problems found', closeOnComplete: false } },
    { id: 'demo-run-live', kind: 'run', at: t(7), run: { runId: 'r2', machine: 'dev-box', requestedBy: 'Portfolio Manager', approval: 'approved', command: 'pnpm test --filter chat', state: 'running', seconds: 7, output: '> chat@0.4.0 test\n ✓ stream.test.ts (14)\n ✓ cards.test.ts (22)\n ⠋ drawer.test.ts …', closeOnComplete: true } },
  ]
}
