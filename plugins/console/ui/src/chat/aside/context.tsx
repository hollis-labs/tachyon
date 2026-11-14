import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Commit } from '../cards/Cards'
import type { StreamBlock } from '../cards/types'
import type { ChatMessage } from '../types'
import type { Bookmark, Topic } from './data'
import { DEFAULT_PANEL } from './registry'
import { grant, isGranted, type GrantChoice } from './grants'

/** A tab id: a built-in ('plan', 'topics', ... 'scratch') or one a registered panel brings (see registry.ts). */
export type PanelId = string
export const PANELS = DEFAULT_PANEL.tabs

/** What the open conversation registers so the aside can show it without a second fetch. */
export interface ConversationData { ref?: string; agent?: string; messages: ChatMessage[]; blocks: StreamBlock[]; blocksLoaded?: boolean; commit?: Commit; onInsert: (text: string) => void; marks: Marks }

export interface Marks { bookmarks: Bookmark[]; toggleBookmark: (messageRef: string) => void; topics: Topic[]; addTopic: (title: string, messageRef: string) => void }

export interface AsideState {
  panel: PanelId
  /** The user changed the view since the agent last did: the agent's next change needs their say-so. */
  userChanged: boolean
  /** An agent request held until the user answers. */
  pending: { panel: PanelId; reason: string } | null
  open: boolean
  full: boolean
  doc: { name: string; text: string; itemId?: string; shared?: boolean } | null
  /** Bumped by server frames so panels refetch. */
  tick: number
  conv: ConversationData | null
}

interface AsideApi extends AsideState {
  /** The user picked a panel. */
  pick: (p: PanelId) => void
  /** The agent or a server frame asks for a panel. Applied at once unless the user typed in the aside (dropped) or changed the view since the agent last did and has not granted this agent in this thread (held as `pending`). */
  auto: (p: PanelId, reason: string) => 'applied' | 'pending' | 'dropped'
  /** Answer the held request. */
  answer: (choice: GrantChoice) => void
  /** The agent and thread the aside is showing; grants are scoped to it. */
  setScope: (scope: string) => void
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
  const [s, setS] = useState<AsideState>({ panel: 'plan', userChanged: false, pending: null, open: wide, full: false, doc: null, tick: 0, conv: null })
  const rootRef = useRef<HTMLElement | null>(null)
  const scope = useRef('')
  const typing = () => {
    const a = document.activeElement
    return !!a && !!rootRef.current?.contains(a) && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName)
  }
  const register = useCallback((conv: ConversationData | null) => setS((x) => ({ ...x, conv })), [])
  const bump = useCallback(() => setS((x) => ({ ...x, tick: x.tick + 1 })), [])
  const latest = useRef(s)
  latest.current = s
  const api = useMemo<AsideApi>(() => ({
    ...s,
    rootRef,
    pick: (panel) => setS((x) => ({ ...x, panel, userChanged: true, pending: null, open: true, full: false })),
    auto: (panel, reason) => {
      if (typing()) return 'dropped'
      if (latest.current.userChanged && !isGranted(scope.current)) { setS((x) => ({ ...x, pending: { panel, reason } })); return 'pending' }
      setS((x) => ({ ...x, panel, userChanged: false, pending: null, open: x.open || wide }))
      return 'applied'
    },
    answer: (choice) => setS((x) => {
      const p = x.pending
      if (!p) return x
      if (choice === 'deny') return { ...x, pending: null }
      if (choice !== 'once') grant(scope.current, choice)
      return { ...x, panel: p.panel, userChanged: false, pending: null, open: true }
    }),
    setScope: (v) => { scope.current = v },
    toggle: () => setS((x) => ({ ...x, open: !x.open })),
    setOpen: (open) => setS((x) => ({ ...x, open })),
    setFull: (full) => setS((x) => ({ ...x, full })),
    openDoc: (doc) => setS((x) => ({ ...x, doc, panel: 'doc', userChanged: true, open: true, full: true })),
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
