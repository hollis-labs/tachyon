import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChatInput } from '@hollis-labs/kit-chat'
import { ThreadView } from '../ThreadView'
import type { ChatMessage } from '../types'

export interface ConversationViewProps {
  /** Changing this focuses the composer (a thread switch). */
  threadId: string
  messages: ChatMessage[]
  agentLabel: string
  loading: boolean
  hasMore: boolean
  onLoadOlder: () => void
  onSend: (body: string) => Promise<void>
  canSend?: boolean
  /** Load or stream failure for the thread. */
  error?: string | null
  /** Failure of the last send. The draft is kept. */
  sendError?: string | null
  empty?: ReactNode
}

/** Messages plus composer for one thread. Transport-free: the host owns the state and the send. */
export function ConversationView({ threadId, messages, agentLabel, loading, hasMore, onLoadOlder, onSend, canSend = true, error, sendError, empty }: ConversationViewProps) {
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const area = useRef<HTMLTextAreaElement>(null)
  useEffect(() => { area.current?.focus() }, [threadId])
  const submit = async (text: string) => {
    const body = text.trim()
    if (!body || busy) return
    setBusy(true)
    try {
      await onSend(body)
      setValue('')
    } catch {
      /* shown through sendError; keep the draft */
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {error && <p role="alert" className="border-b border-danger/30 px-4 py-2 text-xs text-danger">{error}</p>}
      <ThreadView messages={messages} agentLabel={agentLabel} loading={loading} hasMore={hasMore} onLoadOlder={onLoadOlder} empty={empty} />
      <div className="shrink-0 space-y-1 px-3 pb-3 pt-2">
        {sendError && <p role="alert" className="text-xs text-danger">{sendError}</p>}
        <ChatInput
          value={value}
          onValueChange={setValue}
          onSubmit={(v) => void submit(v)}
          busy={busy}
          disabled={!canSend || !!error}
          textareaRef={area}
          aria-label="Message"
          placeholder={canSend ? 'Message. Enter to send, Shift+Enter for a new line' : 'This agent cannot receive messages right now'}
        />
      </div>
    </div>
  )
}
