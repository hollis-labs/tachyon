import { consoleJson } from '../lib/http'
import { spineHub } from './live'
import type { ChatClient, ChatThread, ThreadSession } from './types'

interface ApiThread {
  thread_ref: string
  title?: string
  agent?: { agent_id?: string }
  unread?: number
  last_at?: number
  last_preview?: string
  blocked_on_user?: number
  archived?: boolean
  pinned?: boolean
  plan_progress?: { done: number; total: number } | null
  context?: { used?: number | null; window?: number | null; model?: string | null }
}

const launchOf = (t: ChatThread) => t.agent.chatLaunchId ?? t.agent.agentId

/**
 * Server-side summaries for the agent rows. The chat API lists one thread per session; the conversation list has
 * one row per agent. Per agent: unread is the sum over its threads, the row needs you if any thread is blocked on
 * the operator, the preview and time come from the newest thread. Messages, sending and live updates still go
 * through `base`. If the chat API is unreachable the rows are `base`'s, unchanged.
 */
export function withChatApiSummaries(base: ChatClient, load: () => Promise<ApiThread[]> = fetchApiThreads, markRef: (ref: string) => Promise<unknown> = postRead): ChatClient {
  let byAgent = new Map<string, ApiThread[]>()
  const agentOf = new Map<string, string>()
  const decorate = (threads: ChatThread[]): ChatThread[] =>
    threads
      .map((t) => {
        agentOf.set(t.id, launchOf(t) ?? '')
        const list = byAgent.get(launchOf(t) ?? '')
        if (!list?.length) return t
        const sessions: ThreadSession[] = list.map((a) => ({ ref: a.thread_ref, title: a.title ?? '', lastAt: a.last_at ?? 0, unread: a.unread ?? 0 }))
        const newest = list[0]
        return {
          ...t,
          unread: sessions.reduce((n, s) => n + s.unread, 0),
          needsYou: list.some((a) => (a.blocked_on_user ?? 0) > 0),
          blocked: list.reduce((n, a) => n + (a.blocked_on_user ?? 0), 0),
          pinned: list.some((a) => a.pinned),
          plan: list.find((a) => a.plan_progress && a.plan_progress.total > 0)?.plan_progress ?? undefined,
          context: newest.context ? { used: newest.context.used ?? null, window: newest.context.window ?? null, model: newest.context.model ?? null } : undefined,
          lastAt: Math.max(t.lastAt, newest.last_at ?? 0),
          lastPreview: newest.last_preview?.slice(0, 100) || t.lastPreview,
          sessions,
        }
      })
      .sort((a, b) => b.lastAt - a.lastAt)
  const refresh = async () => {
    try {
      const next = new Map<string, ApiThread[]>()
      for (const a of (await load()).filter((x) => !x.archived).sort((x, y) => (y.last_at ?? 0) - (x.last_at ?? 0))) {
        const id = a.agent?.agent_id
        if (id) next.set(id, [...(next.get(id) ?? []), a])
      }
      byAgent = next
    } catch (e) {
      console.warn('Chat summaries unavailable:', e instanceof Error ? e.message : e)
      byAgent = new Map()
    }
  }
  return {
    ...base,
    async listThreads() {
      const [threads] = await Promise.all([base.listThreads(), refresh()])
      return decorate(threads)
    },
    async markRead(id) {
      await base.markRead(id)
      const refs = (byAgent.get(agentOf.get(id) ?? '') ?? []).filter((a) => (a.unread ?? 0) > 0)
      await Promise.all(refs.map((a) => markRef(a.thread_ref).catch(() => undefined)))
    },
    subscribeThreads(listener, onError) {
      let live = true
      const push = (t: ChatThread[]) => { if (live) listener(decorate(t)) }
      const off = base.subscribeThreads((t) => void refresh().then(() => push(t)), onError)
      // The global spine stream: a `thread` frame (or a reconnect) means a row changed, so re-list now instead of waiting a tick.
      const offSpine = spineHub().subscribe((f) => { if (f.type === 'thread' || f.type === 'reconnected') void base.listThreads().then((t) => refresh().then(() => push(t)), () => undefined) })
      return () => { live = false; off(); offSpine() }
    },
  }
}

const postRead = (ref: string) => consoleJson(`/chat/threads/${encodeURIComponent(ref)}/read`, { method: 'POST' })

async function fetchApiThreads(): Promise<ApiThread[]> {
  const body = await consoleJson<{ threads?: ApiThread[] }>('/chat/threads?limit=200')
  return body.threads ?? []
}
