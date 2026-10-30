// Shapes from the Agents monitor contract (internal/agentsapi/openapi.yaml). Only what the UI reads.
export type SessionState = 'working' | 'waiting' | 'idle' | 'stale' | 'unknown'
export type StaleReason = 'no_events' | 'not_alive' | 'worker_offline'

export interface OneLinerData { verb: string; target: string; tool: string; at: number; calls_this_turn: number }
export interface ContextData { used: number | null; window: number | null; pct: number | null; source: 'stream' | 'unknown'; breakdown: unknown }
export interface TokensData { metered: boolean; input: number | null; output: number | null; cache_read: number | null; cache_write: number | null }

export interface Session {
  id: string
  agent: { urn: string | null; name: string; logical_agent_id: string | null }
  kind: 'chat' | 'process'
  provider: string
  model: string | null
  workroot: string
  state: SessionState
  stale_reason: StaleReason | null
  stale_since: number | null
  needs_you: { reason: 'approval' | 'question' | null; since: number | null }
  started_at: number
  last_activity_at: number | null
  runtime_ms: number
  one_liner: OneLinerData | null
  context: ContextData
  tokens: TokensData
  tool_call_count: number | null
  pending: { approval: boolean; question: boolean; known: boolean }
  assignment: { theme_ref?: string; task_ref?: string; index?: number; count?: number } | null
  spark: number[]
  capabilities: { nudge: boolean; interrupt: { available: boolean; reason: string | null }; restart: boolean }
}

export interface Collector {
  collecting: boolean
  sessions: number
  last_event_at: number | null
  strategy: 'snapshot' | 'fallback'
}

export interface SessionsResponse {
  sessions: Session[]
  summary: Partial<Record<SessionState, number>>
  collector: Collector
}

export type StreamStatus = 'loading' | 'live' | 'reconnecting' | 'error'
