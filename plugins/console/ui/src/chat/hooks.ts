import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import { useChat } from './context'
import { applyChatEvent, mergeMessages, type ChatThreadState } from './reducer'
import type { ChatEvent, ChatMessage, ChatThread } from './types'

const PAGE = 20

type Action = { type: 'event'; e: ChatEvent } | { type: 'history'; messages: ChatMessage[] }
const reduce = (s: ChatThreadState, a: Action): ChatThreadState =>
  a.type === 'event' ? applyChatEvent(s, a.e) : a.type === 'history' ? { ...s, messages: mergeMessages(s.messages, a.messages) } : s

export function useChatThreads() {
  const { client } = useChat()
  const [threads, setThreads] = useState<ChatThread[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [version,setVersion] = useState(0)
  useEffect(() => {
    let live = true
    const accept = (t:ChatThread[]) => { if(live) {setThreads(t);setError(null)} }
    const failed = (e:unknown) => {if(live) setError(e instanceof Error ? e.message : String(e))}
    client.listThreads().then(accept, failed)
    const off = client.subscribeThreads(accept, failed)
    return () => {
      live = false
      off()
    }
  }, [client,version])
  return {threads,error,retry:()=>setVersion(v=>v+1)}
}

/** Total unread across threads. Feed this to the alert tray / a badge. */
export function useChatUnread(): number {
  const {threads} = useChatThreads()
  return (threads ?? []).reduce((n, t) => n + t.unread, 0)
}

export function useChatThread(threadId: string | null) {
  const { client } = useChat()
  const [state, dispatch] = useReducer(reduce, { messages: [] })
  const [loading, setLoading] = useState(!!threadId)
  const [hasMore, setHasMore] = useState(false)
  const [sendError, setSendError] = useState<string | null>(null)
  const oldest = useRef<number | undefined>(undefined)
  const markedAt = useRef(0)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    // State starts fresh per thread: callers key the component on threadId.
    if (!threadId) return
    let live = true
    client.history(threadId, { limit: PAGE }).then(
      (p) => {
        if (!live) return
        dispatch({ type: 'history', messages: p.messages })
        setHasMore(p.hasMore)
        oldest.current = p.messages[0]?.at
        setLoading(false)
        markedAt.current = Math.max(markedAt.current, ...p.messages.map((m) => m.at))
        void client.markRead(threadId)
      },
      (e) => { if (live) { setLoading(false); dispatch({type:'event', e:{type:'error',message:e instanceof Error ? e.message : 'Could not load conversation'}}) } },
    )
    const off = client.subscribe(threadId, (e) => {
      if (!live) return
      dispatch({ type: 'event', e })
      // Every sync re-emits the newest page; mark read once per newest agent message, not once per frame.
      if (e.type === 'message' && e.message.from === 'agent' && e.message.at > markedAt.current) {
        markedAt.current = e.message.at
        void client.markRead(threadId)
      }
    })
    return () => {
      live = false
      off()
    }
  }, [client, threadId, attempt])

  const loadOlder = useCallback(async () => {
    if (!threadId || loading || !hasMore) return
    setLoading(true)
    try {
      const p = await client.history(threadId, { before: oldest.current, limit: PAGE })
      dispatch({ type: 'history', messages: p.messages })
      setHasMore(p.hasMore)
      oldest.current = p.messages[0]?.at ?? oldest.current
    } catch (e) {
      dispatch({type:'event',e:{type:'error',message:e instanceof Error ? e.message : 'Could not load older messages'}})
    } finally {
      setLoading(false)
    }
  }, [client, threadId, loading, hasMore])

  const send = useCallback(
    async (body: string) => {
      if (!threadId) return
      setSendError(null)
      try {
        const m = await client.send(threadId, body)
        dispatch({ type: 'event', e: { type: 'message', message: m } })
      } catch (err) {
        setSendError(err instanceof Error ? err.message : 'Could not send')
        throw err
      }
    },
    [client, threadId],
  )

  const retry = useCallback(() => { setLoading(true); dispatch({ type: 'event', e: { type: 'error', message: null } }); setAttempt((n) => n + 1) }, [])
  return { messages: state.messages, capability: state.capability, loading, hasMore, loadOlder, send, sendError, error: state.error, retry }
}
