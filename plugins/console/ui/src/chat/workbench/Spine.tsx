import { useEffect, useMemo, useRef, useState } from 'react'
import { Search } from 'lucide-react'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Chip, Kbd, SegmentedProgress, SpineList, StateDot, type SpineGroup } from '@planner/shared'
import type { ChatThread } from '../types'

export type Lens = 'all' | 'agents' | 'work' | 'archived'
const LENSES: { id: Lens; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'agents', label: 'Agents' },
  { id: 'work', label: 'Work' },
  { id: 'archived', label: 'Archived' },
]

export interface StartOption { id: string; label: string; onSelect: () => void }

export interface SpineProps {
  /** null while the first load is in flight. */
  threads: ChatThread[] | null
  activeId: string | null
  onSelect: (id: string) => void
  startOptions?: StartOption[]
  error?: string | null
  onRetry?: () => void
  now?: number
}

const DAY = 86_400_000
/** '21m', '15h', '3d': the golden's compact age. */
const short = (at: number, now: number) => { const m = Math.max(0, Math.round((now - at) / 60_000)); return m < 1 ? 'now' : m < 60 ? `${m}m` : m < 2880 ? `${Math.round(m / 60)}h` : `${Math.round(m / 1440)}d` }
const matches = (t: ChatThread, q: string) => `${t.title} ${t.lastPreview ?? ''}`.toLowerCase().includes(q)
const inLens = (t: ChatThread, lens: Lens) => (lens === 'work' ? !!t.plan || (t.blocked ?? 0) > 0 : lens === 'agents' ? t.capability.mode === 'session' || t.capability.mode === 'live' : lens === 'archived' ? false : true)

/** Plan progress as bar items: done, the doing step, the rest to do. */
export const planItems = (p: { done: number; total: number }) => [
  { state: 'done' as const, count: p.done },
  { state: 'doing' as const, count: p.done < p.total ? 1 : 0 },
  { state: 'todo' as const, count: Math.max(0, p.total - p.done - 1) },
]

/** chat.spine: header with Start, search, lenses, the need-you row, then Pinned / Today / Earlier groups. */
export function Spine({ threads, activeId, onSelect, startOptions = [], error, onRetry, now: nowProp }: SpineProps) {
  const [query, setQuery] = useState('')
  const [lens, setLens] = useState<Lens>('all')
  const [onlyBlocked, setOnlyBlocked] = useState(false)
  const [startOpen, setStartOpen] = useState(false)
  const [mount] = useState(() => Date.now())
  const now = nowProp ?? mount
  const search = useRef<HTMLInputElement>(null)
  const q = query.trim().toLowerCase()

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return
      if (e.key.toLowerCase() === 'k') { e.preventDefault(); search.current?.focus() }
      else if (e.key.toLowerCase() === 'n' && startOptions.length > 0) { e.preventDefault(); setStartOpen(true) }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [startOptions.length])

  const sum = (k: 'approvals' | 'questions') => (threads ?? []).reduce((n, t) => n + (t.blockedBreakdown?.[k] ?? 0), 0)
  const hasSplit = (threads ?? []).some((t) => t.blockedBreakdown)
  const blockedTotal = (threads ?? []).reduce((n, t) => n + (t.blocked ?? 0), 0)
  const shown = useMemo(() => (threads ?? []).filter((t) => inLens(t, lens) && (!q || matches(t, q)) && (!onlyBlocked || (t.blocked ?? 0) > 0)), [threads, lens, q, onlyBlocked])

  const groups = useMemo<SpineGroup[]>(() => {
    const entry = (t: ChatThread) => ({
      id: t.id,
      title: t.title,
      leading: undefined,
      flags: (
        <>
          {(t.blocked ?? 0) > 0 ? <StateDot state="blocked" className="self-center" /> : t.unread > 0 ? <StateDot state="doing" className="self-center" /> : null}
          {t.lastAt > 0 && !t.pinned && <span className="font-mono text-caption text-fg-faint">{short(t.lastAt, now)}</span>}
        </>
      ),
      meta: (t.plan || t.lastPreview) && (
        <span className="truncate text-xs text-fg-muted">{[t.lastPreview, t.plan ? `plan ${t.plan.done} of ${t.plan.total}` : ''].filter(Boolean).join(' · ')}</span>
      ),
      progress: t.plan && t.plan.total > 0 ? <SegmentedProgress size="sm" items={planItems(t.plan)} label={`Plan ${t.plan.done} of ${t.plan.total} done`} className="h-[3px]" /> : undefined,
    })
    const pinned = shown.filter((t) => t.pinned)
    const rest = shown.filter((t) => !t.pinned)
    const today = rest.filter((t) => now - t.lastAt < DAY)
    const earlier = rest.filter((t) => now - t.lastAt >= DAY)
    return [
      { id: 'pinned', title: 'Pinned', items: pinned.map(entry) },
      { id: 'today', title: 'Today', items: today.map(entry) },
      { id: 'earlier', title: 'Earlier', items: earlier.map(entry) },
    ].filter((g) => g.items.length > 0)
  }, [shown, now])

  return (
    <nav aria-label="Chats" className="flex min-h-0 w-full flex-col">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border-subtle px-3">
        <h2 className={CAPTION}>Chats</h2>
        <span className="flex-1" />
        <div className="relative">
          <button
            type="button"
            onClick={() => setStartOpen((o) => !o)}
            disabled={startOptions.length === 0}
            aria-haspopup="menu"
            aria-expanded={startOpen}
            className="inline-flex h-[22px] items-center gap-1.5 rounded-control border border-primary bg-primary px-2.5 text-label font-semibold text-primary-fg disabled:opacity-50 focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring"
          >
            + Start <Kbd className="border-primary-fg/30 text-primary-fg">⌘N</Kbd>
          </button>
          {/* KIT-GAP: a menu popover with a keyboard-hint column; hand-rolled here until the kit has one. */}
          {startOpen && (
            <div role="menu" aria-label="Start a chat with" className="absolute right-0 top-full z-20 mt-1 max-h-72 min-w-52 overflow-y-auto rounded-panel border border-surface-hover bg-bg-elevated p-1 shadow-lg">
              {startOptions.map((o) => (
                <button key={o.id} type="button" role="menuitem" onClick={() => { setStartOpen(false); o.onSelect() }} className="flex h-7 w-full items-center rounded-sm px-2 text-left text-control text-fg-secondary hover:bg-selection hover:text-fg focus-visible:bg-selection focus-visible:outline-none">
                  {o.label}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
      <label className="mx-2 mt-2 flex h-7 items-center gap-2 rounded-control border border-border px-2.5 text-xs text-fg-muted focus-within:outline focus-within:outline-1 focus-within:outline-ring">
        <Search aria-hidden className="size-3.5 shrink-0" />
        <input ref={search} value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === 'Escape') { setQuery(''); e.currentTarget.blur() } }} aria-label="Search chats and messages" placeholder="Search chats and messages" className="min-w-0 flex-1 rounded-none border-0 bg-transparent p-0 text-xs text-fg placeholder:text-fg-muted focus-visible:bg-transparent!" />
        <Kbd className="ml-auto">⌘K</Kbd>
      </label>
      <div role="tablist" aria-label="Lens" className="flex gap-1 px-2 pb-1 pt-2">
        {LENSES.map((l) => (
          <button key={l.id} type="button" role="tab" aria-selected={lens === l.id} onClick={() => setLens(l.id)} className={cn('inline-flex h-[22px] items-center rounded-full px-2 text-label text-fg-muted hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', lens === l.id && 'border border-border bg-selection text-fg')}>
            {l.label}
          </button>
        ))}
      </div>
      {blockedTotal > 0 && (
        <button type="button" aria-pressed={onlyBlocked} onClick={() => setOnlyBlocked((v) => !v)} className={cn('mx-2 my-1 flex h-7 items-center gap-2 whitespace-nowrap rounded-control border border-border px-2.5 text-xs text-fg-secondary hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', onlyBlocked && 'bg-selection text-fg')}>
          <StateDot state="blocked" />
          {blockedTotal} need you
          {hasSplit ? (
            <span className="ml-auto flex gap-1.5">
              {sum('approvals') > 0 && <Chip square>approve {sum('approvals')}</Chip>}
              {sum('questions') > 0 && <Chip square>answer {sum('questions')}</Chip>}
            </span>
          ) : <Chip square className="ml-auto">answer {blockedTotal}</Chip>}
        </button>
      )}
      {error && (
        <div role="alert" className="px-3 py-2 text-xs text-danger">
          {error}
          {onRetry && <button type="button" onClick={onRetry} className="ml-2 underline">Retry</button>}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        {threads === null ? (
          !error && <p role="status" className="px-2 py-3 text-xs text-fg-faint">Loading chats…</p>
        ) : threads.length === 0 ? (
          <p className="px-2 py-3 text-xs text-fg-muted">No chats yet. Start one with an agent.</p>
        ) : lens === 'archived' ? (
          <p className="px-2 py-3 text-xs text-fg-muted">No archived chats.</p>
        ) : shown.length === 0 ? (
          <p className="px-2 py-3 text-xs text-fg-muted">{q ? `Nothing matches “${query.trim()}”.` : 'Nothing here.'}</p>
        ) : (
          <SpineList groups={groups} selectedId={activeId ?? undefined} onSelect={onSelect} label="Chats" />
        )}
      </div>
    </nav>
  )
}
