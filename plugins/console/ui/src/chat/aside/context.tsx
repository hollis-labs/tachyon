import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Commit } from '../cards/Cards'
import type { StreamBlock } from '../cards/types'
import type { ChatMessage } from '../types'
import type { Bookmark, Topic } from './data'

export type PanelId = 'plan' | 'topics' | 'bookmarks' | 'work' | 'activity' | 'approvals' | 'files' | 'doc' | 'scratch'
export const PANELS: { id: PanelId; label: string }[] = [
  { id: 'plan', label: 'Plan' }, { id: 'topics', label: 'Topics' }, { id: 'bookmarks', label: 'Bookmarks' }, { id: 'work', label: 'Work' },
  { id: 'activity', label: 'Activity' }, { id: 'approvals', label: 'Approvals' }, { id: 'files', label: 'Files' }, { id: 'doc', label: 'Doc' },
]

/** What the open conversation registers so the aside can show it without a second fetch. */
export interface ConversationData { ref?: string; agent?: string; messages: ChatMessage[]; blocks: StreamBlock[]; blocksLoaded?: boolean; commit?: Commit; onInsert: (text: string) => void; marks: Marks }

export interface Marks { bookmarks: Bookmark[]; toggleBookmark: (messageRef: string) => void; topics: Topic[]; addTopic: (title: string, messageRef: string) => void }

export interface AsideState {
  panel: PanelId
  /** 'auto' follows the agent; a manual pick locks until the user returns to auto. */
  mode: 'auto' | 'manual'
  reason: string
  open: boolean
  full: boolean
  doc: { name: string; text: string; itemId?: string; shared?: boolean } | null
  /** Bumped by server frames so panels refetch. */
  tick: number
  conv: ConversationData | null
}

interface AsideApi extends AsideState {
  /** The user picked a panel: locks auto. */
  pick: (p: PanelId) => void
  /** The agent or a server frame asks for a panel. Never steals focus and never switches while the user types in the aside or after a manual pick. */
  auto: (p: PanelId, reason: string) => void
  backToAuto: () => void
  toggle: () => void
  setOpen: (o: boolean) => void
  setFull: (f: boolean) => void
  openDoc: (d: AsideState['doc']) => void
  bump: () => void
  register: (c: ConversationData | null) => void
  /** Ref the host sets on the aside root so auto can tell whether the user is typing in it. */
  rootRef: React.RefObject<HTMLElement | null>
}

const Ctx = createContext<AsideApi | null>(null)

export function AsideProvider({ children, wide }: { children: ReactNode; wide: boolean }) {
  const [s, setS] = useState<AsideState>({ panel: 'plan', mode: 'auto', reason: '', open: wide, full: false, doc: null, tick: 0, conv: null })
  const rootRef = useRef<HTMLElement | null>(null)
  const typing = () => {
    const a = document.activeElement
    return !!a && !!rootRef.current?.contains(a) && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName)
  }
  const register = useCallback((conv: ConversationData | null) => setS((x) => ({ ...x, conv })), [])
  const bump = useCallback(() => setS((x) => ({ ...x, tick: x.tick + 1 })), [])
  const api = useMemo<AsideApi>(() => ({
    ...s,
    rootRef,
    pick: (panel) => setS((x) => ({ ...x, panel, mode: 'manual', reason: 'you chose', open: true, full: panel === 'doc' })),
    auto: (panel, reason) => {
      if (typing()) return
      setS((x) => (x.mode === 'manual' ? x : { ...x, panel, reason, open: x.open || x.mode === 'auto' && wide }))
    },
    backToAuto: () => setS((x) => ({ ...x, mode: 'auto', reason: '' })),
    toggle: () => setS((x) => ({ ...x, open: !x.open })),
    setOpen: (open) => setS((x) => ({ ...x, open })),
    setFull: (full) => setS((x) => ({ ...x, full })),
    openDoc: (doc) => setS((x) => ({ ...x, doc, panel: 'doc', mode: 'manual', reason: 'you chose', open: true, full: true })),
    bump,
    register,
  }), [s, wide, bump, register])
  return <Ctx.Provider value={api}>{children}</Ctx.Provider>
}

export function useAside() {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAside outside AsideProvider')
  return c
}
/** Same, but null outside the page (the dock has no aside). */
export const useAsideOptional = () => useContext(Ctx)

/** Register the open conversation with the aside while mounted. */
export function useRegisterConversation(c: ConversationData) {
  const reg = useAsideOptional()?.register
  const key = `${c.ref}|${c.agent}|${c.messages.length}|${c.messages.at(-1)?.id}|${c.blocksLoaded}|${c.blocks.map((b) => b.id).join(',')}|${c.marks.bookmarks.map((b) => b.id).join(',')}|${c.marks.topics.map((t) => t.id).join(',')}`
  const latest = useRef(c)
  latest.current = c
  // biome-ignore lint/correctness/useExhaustiveDependencies: key summarises c
  useEffect(() => { reg?.(latest.current) }, [key, reg])
  useEffect(() => () => reg?.(null), [reg])
}
