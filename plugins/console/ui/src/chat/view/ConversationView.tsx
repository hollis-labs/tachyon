import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Composer } from '../workbench/Composer'
import { ChatPalette, type PaletteActions } from '../workbench/Palette'
import { getDraft, setDraft } from '../drafts'
import { ThreadView } from '../ThreadView'
import type { ChatMessage } from '../types'
import type { Commit } from '../cards/Cards'
import type { StreamBlock } from '../cards/types'

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
  /** Strip above the messages: reachability, presence. */
  status?: ReactNode
  /** Left of the send button, e.g. the reply filter. */
  toolbarStart?: ReactNode
  /** Short caption beside the send button, e.g. 'Mail only'. */
  note?: string | null
  /** Palette sources; the palette is the '/' command surface of the composer. */
  palette?: PaletteActions
  /** Cards, tool-call rows and run cards between the messages. */
  blocks?: StreamBlock[]
  commit?: Commit
  onDrawer?: (mode: 'scratch' | 'terminal' | 'workspace') => void
}

/** Messages plus composer for one thread. Transport-free: the host owns the state and the send. */
export function ConversationView({ threadId, messages, agentLabel, loading, hasMore, onLoadOlder, onSend, canSend = true, error, sendError, empty, status, toolbarStart, note, palette, onDrawer, blocks, commit }: ConversationViewProps) {
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [value, setValueState] = useState(() => getDraft(threadId))
  const setValue = (v: string) => { setValueState(v); setDraft(threadId, v) }
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
      {status}
      {error && <p role="alert" className="border-b border-danger/30 px-4 py-2 text-xs text-danger">{error}</p>}
      <ThreadView messages={messages} agentLabel={agentLabel} loading={loading} hasMore={hasMore} onLoadOlder={onLoadOlder} empty={empty} blocks={blocks} commit={commit} />
      <Composer
        value={value}
        onChange={setValue}
        onSubmit={(v) => void submit(v)}
        agentLabel={agentLabel}
        busy={busy}
        disabled={!canSend || !!error}
        note={note}
        sendError={sendError}
        textareaRef={area}
        onOpenPalette={() => setPaletteOpen(true)}
        onDrawer={onDrawer}
        extra={toolbarStart}
      />
      {palette && <ChatPalette open={paletteOpen} onOpenChange={setPaletteOpen} a={palette} />}
    </div>
  )
}
