// Blocks the stream renders between messages: the answerable cards, the tool-call row and the terminal run
// card. Cards map from the chat API Card projection (internal/chatapi/openapi.yaml); the run card from Run.

export type CardPhase = 'pending' | 'saving' | 'answered' | 'error'

export interface DecisionOption {
  id: string
  label: string
  description?: string
  recommended?: boolean
}

export interface PlanStep {
  id: string
  title: string
  state: 'done' | 'doing' | 'todo'
}

export interface RunView {
  runId: string
  machine: string
  requestedBy: string
  approval: 'approved' | 'policy'
  command: string
  state: 'running' | 'finished' | 'failed' | 'stopped'
  exitCode?: number
  /** Seconds the run took (finished) or has taken (running). */
  seconds: number
  output: string
  closeOnComplete: boolean
  closed?: boolean
  truncated?: boolean
}

interface Base {
  id: string
  at: number
  /** The Approvals queue item the answer goes to; absent for fixtures and read-only blocks. */
  itemId?: string
}

export type StreamBlock =
  | (Base & { kind: 'decision'; title: string; cite?: string; options: DecisionOption[]; reversible: boolean; answered?: { optionId: string; note?: string; next?: string } })
  | (Base & { kind: 'plan'; title: string; steps: PlanStep[]; total: number; done: number; doing: number })
  | (Base & { kind: 'approval'; title: string; tool: string; count?: number; canAlways: boolean; reversible: boolean; answered?: { decision: 'approved' | 'always' | 'denied'; ran?: boolean } })
  | (Base & { kind: 'dispatch'; target: string; effect: string; fields: { title: string; project: string; priority: string }; answered?: { taskId?: string } })
  | (Base & { kind: 'tools'; tools: string[]; state: 'running' | 'done' | 'failed' })
  | (Base & { kind: 'run'; run: RunView })

export type BlockKind = StreamBlock['kind']
