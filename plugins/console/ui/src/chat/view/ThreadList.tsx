import { useMemo, useState } from 'react'
import { EmptyState, OverflowMenu, SearchInput, type OverflowAction } from '@hollis-labs/design-components'
import { useListNavigation } from '@hollis-labs/kit-dashboard/data'
import { MessageSquare, Mail } from 'lucide-react'
import { ago } from '@console/lib/agent-activity'
import { CapabilityBadge } from '../CapabilityBadge'
import type { ChatThread } from '../types'

export interface ThreadListProps {
  /** null while the first load is in flight. */
  threads: ChatThread[] | null
  activeId: string | null
  onSelect: (id: string) => void
  /** One action per agent a new conversation can start with. Empty hides the control. */
  newChatActions?: OverflowAction[]
  error?: string | null
  onRetry?: () => void
  /** Epoch ms used for relative times; defaults to mount time. */
  now?: number
}

const matches = (t: ChatThread, q: string) => `${t.title} ${t.lastPreview ?? ''}`.toLowerCase().includes(q)

/** Searchable, keyboard-navigable conversation list. Transport-free: the host owns the threads. */
export function ThreadList({ threads, activeId, onSelect, newChatActions = [], error, onRetry, now: nowProp }: ThreadListProps) {
  const [query, setQuery] = useState('')
  const [mount] = useState(() => Date.now())
  const now = nowProp ?? mount
  const q = query.trim().toLowerCase()
  const shown = useMemo(() => (threads ?? []).filter((t) => !q || matches(t, q)), [threads, q])
  const nav = useListNavigation({ ids: shown.map((t) => t.id), currentId: activeId, onNavigate: onSelect })
  const go = (id: string | undefined) => {
    if (!id) return
    onSelect(id)
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-thread-id="${CSS.escape(id)}"]`)?.focus())
  }
  return (
    <section aria-label="Conversations" className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b border-border px-3 py-2">
        {/* dash-search-adapter strips the global input border/padding from SearchInput's inner <input>; without it the field is a box in a box. */}
        <div className="dash-search-adapter min-w-0 flex-1">
          <SearchInput value={query} onChange={setQuery} ariaLabel="Search conversations" placeholder="Search conversations" slashToFocus={false} />
        </div>
        {newChatActions.length > 0 && <OverflowMenu ariaLabel="New chat" actions={newChatActions} align="end" />}
      </div>
      {error && (
        <div role="alert" className="px-3 py-3 text-xs text-danger">
          {error}
          {onRetry && <button type="button" onClick={onRetry} className="ml-2 underline">Retry</button>}
        </div>
      )}
      {threads === null ? (
        !error && <p className="px-3 py-4 text-caption text-fg-faint">Loading</p>
      ) : threads.length === 0 ? (
        <EmptyState variant="empty" title="No conversations" description={newChatActions.length > 0 ? 'Start one with an agent from the menu above.' : 'Open one from an agent.'} />
      ) : shown.length === 0 ? (
        <EmptyState variant="no-results" title="No matches" description={`Nothing matches "${query.trim()}".`} />
      ) : (
        <ul
          aria-label="Threads"
          className="min-h-0 flex-1 overflow-y-auto"
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); go(nav.nextId) }
            else if (e.key === 'ArrowUp') { e.preventDefault(); go(nav.previousId) }
          }}
        >
          {shown.map((t) => {
            const active = t.id === activeId
            const live = t.capability.mode === 'session' || t.capability.mode === 'live'
            return (
              <li key={t.id} className="border-b border-border/80">
                <button
                  type="button"
                  data-thread-id={t.id}
                  aria-current={active ? 'true' : undefined}
                  onClick={() => onSelect(t.id)}
                  className={`block w-full px-3 py-2 text-left hover:bg-surface/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring ${active ? 'bg-surface' : ''}`}
                >
                  <div className="flex items-center justify-between gap-3">
                    <span className="flex min-w-0 items-center gap-2 text-control text-fg">
                      {live ? <MessageSquare aria-label="Chat" className="h-3.5 w-3.5 shrink-0 text-success" /> : <Mail aria-label="Email" className="h-3.5 w-3.5 shrink-0 text-info" />}
                      <span className="truncate">{t.title}</span>
                    </span>
                    {t.unread > 0 && <span aria-label={`${t.unread} unread`} className="border border-primary/60 px-1.5 font-mono dash-caption text-primary">{t.unread}</span>}
                  </div>
                  <div className="mt-1 flex items-center justify-between gap-3">
                    <CapabilityBadge capability={t.capability} />
                    {t.lastAt > 0 && <span className="font-mono dash-caption text-fg-faint">{ago(t.lastAt, now)}</span>}
                  </div>
                  {t.lastPreview && <p className="mt-1 truncate dash-label text-fg-faint">{t.lastPreview}</p>}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
