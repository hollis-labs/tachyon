// Console chat domain types. Independent of lib/types.ts: the mail adapter
// (mail.ts) maps onto MessagingClient, a future session-stream adapter maps
// the same ChatEvent union from Outpost/Nanite chat-stream frames. Event
// names here are placeholders; keep mappings in adapters, not components.
// Additive only. See BACKEND_NEEDS.md.

import type { DeliveryState, Recipient } from '../messaging'

/** How we can actually reach the agent right now. Shown in the UI, never hidden. */
export type ChatMode = 'live' | 'mail-only' | 'offline' | 'session'

export interface ChatCapability {
  mode: ChatMode
  canSend?: boolean
  /** Why: "attached to session s-12 via Outpost hook", "no stream registered". */
  detail?: string
  /** Session id when attached to a hook-managed session. */
  sessionId?: string
}

export interface ChatThread {
  id: string
  agent: Recipient
  title: string
  capability: ChatCapability
  unread: number
  lastAt: number
  lastPreview?: string
  /** Any of this agent's sessions is blocked on the operator (chat API `blocked_on_user`). */
  needsYou?: boolean
  /** The agent's sessions, newest first (chat API threads). Absent when the chat API has none. */
  sessions?: ThreadSession[]
  /** Any of this agent's sessions is pinned. */
  pinned?: boolean
  /** Count of open cards and questions waiting on the operator, summed over the agent's sessions. */
  blocked?: number
  /** `blocked` split by what the operator must do: approve (approval turns and cards) or answer (the rest). */
  blockedBreakdown?: { approvals: number; questions: number }
  /** Plan progress of the newest session that has a plan. */
  plan?: { done: number; total: number }
  /** Context window use of the newest session (chat API `context`). */
  context?: { used: number | null; window: number | null; model: string | null }
  /** The session shown in the conversation (a `sessions` ref). Absent: the agent's own thread. */
  sessionRef?: string
}

export interface ThreadSession {
  ref: string
  title: string
  lastAt: number
  unread: number
}

export type ToolChipStatus = 'running' | 'ok' | 'error'

export interface ToolChip {
  id: string
  /** Tool or step name, e.g. "torque_task_get". */
  name: string
  status: ToolChipStatus
  /** Short, already-redacted summary. */
  detail?: string
}

export type ChatSender = 'operator' | 'agent' | 'system'

/**
 * Who asked for the turn behind an agent reply, as Tether proved it
 * (docs/reply-audience.md on tether). 'unknown' covers every reply Tether
 * could not attribute: older replies, queued inputs, sends made with the
 * shared operator token.
 */
export type ReplyAudience = 'operator' | 'agent' | 'system' | 'unknown'

export interface ChatMessage {
  id: string
  threadId: string
  from: ChatSender
  /** Agent replies from a Tether turn only; absent means the transport does not say. */
  audience?: ReplyAudience
  body: string
  at: number
  /** Operator messages only. */
  delivery?: DeliveryState
  /** True while tokens are still arriving. */
  streaming?: boolean
  tools: ToolChip[]
  error?: string
  info?: {
    transport: 'tether-chat' | 'mail'
    agentName?: string; launchId?: string; sessionId?: string; turnId?: string
    kind?: string; delivery?: string; sender?: string; channel?: string
    publicationId?: string; sequence?: number; outputId?: string; runtime?: string; idempotencyKey?: string
    trace?: Array<{ stage_id: string; stage_version: string; outcome: string; duration_ms: number; failure_code?: string }>
  }
}

/** Everything that can happen in a thread. Applied by `applyChatEvent`. */
export type ChatEvent =
  | { type: 'error'; message: string | null }
  | { type: 'message'; message: ChatMessage }
  | { type: 'delta'; messageId: string; text: string }
  | { type: 'tool'; messageId: string; tool: ToolChip }
  | { type: 'end'; messageId: string; error?: string }
  | { type: 'delivery'; messageId: string; state: DeliveryState }
  | { type: 'capability'; capability: ChatCapability }

export interface HistoryQuery {
  /** Return messages older than this epoch ms (exclusive). */
  before?: number
  limit?: number
}

export interface HistoryPage {
  /** Oldest first. */
  messages: ChatMessage[]
  hasMore: boolean
}

/** Reserved. See ChatClient.send and DescribeView. */
export interface SendOptions {
  context?: Record<string, unknown>
}

/**
 * Reserved hook: a route returns a small, redacted description of what the operator
 * is looking at, to be passed as `SendOptions.context`. Nothing calls it yet.
 */
export type DescribeView = () => Record<string, unknown> | null

/**
 * Seam for console chat. The mock implements it today; the mail impl wraps a
 * MessagingClient (no streaming); a session impl follows when the Nanite
 * chat-stream / Outpost hook lands (BACKEND_NEEDS.md).
 */
export interface ChatClient {
  listThreads(): Promise<ChatThread[]>
  /** Find or create the conversation with an agent. */
  openThread(agent: Recipient): Promise<ChatThread>
  history(threadId: string, q?: HistoryQuery): Promise<HistoryPage>
  /**
   * Sends an operator message; further updates arrive through subscribe.
   * `opts.context` is reserved for the app agent (what the user is viewing).
   * No client reads it yet.
   */
  send(threadId: string, body: string, opts?: SendOptions): Promise<ChatMessage>
  markRead(threadId: string): Promise<void>
  /** Stream events for one thread. Returns an unsubscribe. */
  subscribe(threadId: string, listener: (e: ChatEvent) => void): () => void
  /** Thread list changes (new message, unread count). */
  subscribeThreads(listener: (threads: ChatThread[]) => void, onError?: (message: string) => void): () => void
}
