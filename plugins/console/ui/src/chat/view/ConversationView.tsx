import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Composer } from '../workbench/Composer'
import { Drawer } from '../drawer/Drawer'
import { ScratchPane } from '../drawer/ScratchPane'
import { TerminalPane } from '../drawer/TerminalPane'
import { WorkspacePane } from '../drawer/WorkspacePane'
import { useScratchPlacement, useWorkspace, type DrawerMode, type DrawerSize } from '../drawer/model'
import { DEMO } from '../cards/useBlocks'
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
  /** The chat API thread ref behind the conversation (workspace, scratchpad, terminal are keyed by it). */
  threadRef?: string
}

/** Messages plus composer for one thread. Transport-free: the host owns the state and the send. */
export function ConversationView({ threadId, messages, agentLabel, loading, hasMore, onLoadOlder, onSend, canSend = true, error, sendError, empty, status, toolbarStart, note, palette, blocks, commit, threadRef }: ConversationViewProps) {
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [value, setValueState] = useState(() => getDraft(threadId))
  const setValue = (v: string) => { setValueState(v); setDraft(threadId, v) }
  const [busy, setBusy] = useState(false)
  const [drawer, setDrawer] = useState<DrawerMode | null>(null)
  const [size, setSize] = useState<DrawerSize>('half')
  const ws = useWorkspace(threadRef)
  const placement = useScratchPlacement()
  // Opening the open mode again closes it (the composer icons and the keys toggle).
  const openDrawer = (m: DrawerMode) => setDrawer((cur) => (cur === m ? null : m))
  const insert = (text: string) => { setValue(value ? `${value}\n${text}` : text); setDrawer(null); area.current?.focus() }
  const area = useRef<HTMLTextAreaElement>(null)
  useEffect(() => { area.current?.focus() }, [threadId])
  useEffect(() => { setDrawer(null) }, [threadId])
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const k = e.key.toLowerCase()
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && k === 'j' && placement === 'drawer') { e.preventDefault(); openDrawer('scratch') }
      else if (e.ctrlKey && !e.metaKey && !e.shiftKey && k === 't') { e.preventDefault(); openDrawer('terminal') }
      else if ((e.metaKey || e.ctrlKey) && e.shiftKey && k === 'w') { e.preventDefault(); openDrawer('workspace') }
      else if (e.key === 'Escape' && drawer && !e.defaultPrevented && !(e.target instanceof Element && e.target.closest('[role=dialog], [role=menu]'))) { setDrawer(null); area.current?.focus() }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [drawer, placement])
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
      {drawer && (
        <Drawer mode={drawer} size={size} onSize={setSize} onClose={() => { setDrawer(null); area.current?.focus() }}>
          {drawer === 'scratch' && <ScratchPane threadRef={threadRef} onInsert={insert} />}
          {drawer === 'terminal' && <TerminalPane threadRef={threadRef} demo={DEMO} />}
          {drawer === 'workspace' && <WorkspacePane ws={ws} />}
        </Drawer>
      )}
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
        onDrawer={openDrawer}
        drawerMode={drawer}
        strip={ws.items.filter((i) => i.shared).map((i) => ({ id: i.id, label: i.name }))}
        onRemoveStrip={(id) => ws.share(id, false)}
        extra={toolbarStart}
      />
      {palette && <ChatPalette open={paletteOpen} onOpenChange={setPaletteOpen} a={{ ...palette, onDrawer: openDrawer }} />}
    </div>
  )
}
