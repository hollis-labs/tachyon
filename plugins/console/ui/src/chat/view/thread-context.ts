import { useEffect, useState } from 'react'
import { consoleJson } from '../../lib/http'
import type { ChatThread } from '../types'

export interface ContextActivity { seq: number; type: string; actor: string; at: number; title?: string; summary?: string }
export interface ContextFile { id: string; name: string; type: string; sizeBytes?: number | null }
export interface ContextWork { id: string; kind: string; title: string; status?: string; needsUser?: boolean }
export interface ThreadContext {
  /** The live chat thread this conversation maps to; null when the host has none for it yet. */
  ref: string | null
  activity: ContextActivity[]
  files: ContextFile[]
  work: ContextWork[]
  state: 'loading' | 'ready' | 'unavailable'
}

interface ActivityRow { seq: number; type: string; actor: string; at: number; payload?: { title?: string; summary?: string } }
interface FileRow { id: string; name: string; type: string; size_bytes?: number | null }
interface WorkRow { id: string; kind: string; title: string; status?: string; needs_user?: boolean }

const none: ThreadContext = { ref: null, activity: [], files: [], work: [], state: 'loading' }

/** An unknown route or a thread with nothing yet is an empty list, not an error. */
async function listOr<T>(path: string, pick: (v: never) => T[]): Promise<T[]> {
  try {
    return pick((await consoleJson<unknown>(path)) as never)
  } catch (e) {
    if ((e as { status?: number }).status === 404) return []
    throw e
  }
}

/**
 * Real context for the open conversation from the chat API: its activity, shared files and linked work.
 * The conversation here is keyed by agent; the chat API by session thread, so the agent's newest session is used
 * (`thread.sessions`, set from the chat API list).
 */
export function useThreadContext(thread: ChatThread | null): ThreadContext {
  const [ctx, setCtx] = useState<ThreadContext>(none)
  const key = thread?.id
  useEffect(() => {
    if (!thread) return setCtx(none)
    let live = true
    setCtx(none)
    ;(async () => {
      try {
        const ref = thread.sessions?.[0]?.ref ?? null
        if (!ref) return live && setCtx({ ...none, state: 'ready' })
        const enc = encodeURIComponent(ref)
        const [activity, files, work] = await Promise.all([
          listOr<ContextActivity>(`/chat/threads/${enc}/activity?limit=10`, (v: { activity?: ActivityRow[] }) => (v.activity ?? []).map((a) => ({ seq: a.seq, type: a.type, actor: a.actor, at: a.at, title: a.payload?.title, summary: a.payload?.summary }))),
          listOr<ContextFile>(`/chat/threads/${enc}/workspace`, (v: { items?: FileRow[] }) => (v.items ?? []).map((f) => ({ id: f.id, name: f.name, type: f.type, sizeBytes: f.size_bytes }))),
          listOr<ContextWork>(`/chat/threads/${enc}/work`, (v: { items?: WorkRow[] }) => (v.items ?? []).map((w) => ({ id: w.id, kind: w.kind, title: w.title, status: w.status, needsUser: w.needs_user }))),
        ])
        if (live) setCtx({ ref, activity, files, work, state: 'ready' })
      } catch {
        if (live) setCtx({ ...none, state: 'unavailable' })
      }
    })()
    return () => { live = false }
  }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  return ctx
}
