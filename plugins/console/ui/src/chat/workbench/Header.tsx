import { MoreHorizontal, Pin, PanelRight } from 'lucide-react'
import { ChevronLeft } from 'lucide-react'
import { OverflowMenu, cn, type OverflowAction } from '@hollis-labs/design-components'
import { Chip } from '@planner/shared'
import type { ChatThread } from '../types'

const kilo = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}K` : String(n))
const initials = (s: string) => s.split(/[\s-]+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join('') || '?'

/**
 * Context window use, text 'N of M tokens' beside a thin bar.
 * KIT-GAP: @planner/shared names a ContextMeter but does not export one yet; this is the chat-local stand-in.
 */
export function ContextMeter({ used, window: win, className }: { used: number | null; window: number | null; className?: string }) {
  const known = used != null && win != null && win > 0
  const pct = known ? Math.min(100, Math.round((used / win) * 100)) : 0
  return (
    <span className={cn('inline-flex h-[26px] items-center gap-2 rounded-control border border-border px-2.5 font-mono text-label text-fg-secondary', className)}>
      {known ? (
        <>
          <span>{kilo(used)} of {kilo(win)} tokens</span>
          <span role="progressbar" aria-label="Context used" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="relative h-[3px] w-12 rounded-sm bg-surface-hover">
            <span className="absolute inset-y-0 left-0 rounded-sm bg-fg/60" style={{ width: `${pct}%` }} />
          </span>
        </>
      ) : (
        <span className="text-fg-muted">Context unknown</span>
      )}
    </span>
  )
}

export interface HeaderProps {
  thread: ChatThread
  /** Session switcher: the agent's other sessions. */
  onSelectSession?: (ref: string) => void
  onTogglePin?: () => void
  onBack?: () => void
  /** Details toggle, shown below 1024px (the aside is an overlay or bottom sheet there). */
  onDetails?: () => void
  detailsOpen?: boolean
  actions?: OverflowAction[]
}

/** chat.header: avatar tile, name, role and model chips, context meter, pin, overflow. */
export function Header({ thread, onSelectSession, onTogglePin, onBack, onDetails, detailsOpen, actions = [] }: HeaderProps) {
  const role = thread.agent.agentId ?? thread.agent.label
  const model = thread.context?.model
  const sessions = thread.sessions ?? []
  const active = thread.sessionRef ?? sessions[0]?.ref
  const switchActions: OverflowAction[] = sessions.length > 1 ? sessions.slice(0, 8).map((s) => ({ label: `${s.title || s.ref}${s.ref === active ? ' (open)' : ''}`, onSelect: () => onSelectSession?.(s.ref) })) : []
  return (
    <header className="flex h-12 shrink-0 items-center gap-2.5 border-b border-border-subtle px-4 max-sm:gap-2 max-sm:px-2.5">
      {onBack && (
        <button type="button" onClick={onBack} aria-label="Back to chats" className="inline-flex h-7 items-center rounded-control border border-border px-2 text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
          <ChevronLeft className="size-4" aria-hidden />
        </button>
      )}
      <span aria-hidden className="grid size-7 place-items-center rounded-control bg-surface font-mono text-xs font-semibold">{initials(thread.title)}</span>
      <h1 className="m-0 flex min-w-0 items-center gap-1 text-control font-semibold normal-case tracking-normal text-fg">
        <span className="truncate">{thread.title}</span>
        {switchActions.length > 0 && <OverflowMenu ariaLabel="Switch session" actions={switchActions} align="start" />}
      </h1>
      <span className="max-sm:hidden"><Chip square>{role}</Chip></span>
      {model && <span className="font-mono text-label text-fg-muted max-sm:hidden">{model}</span>}
      <span className="flex-1" />
      <ContextMeter used={thread.context?.used ?? null} window={thread.context?.window ?? null} className="max-sm:hidden" />
      <button type="button" onClick={onTogglePin} aria-pressed={!!thread.pinned} className={cn('inline-flex h-5 items-center gap-1.5 rounded-full border border-border px-2 text-label text-fg-secondary hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring max-sm:hidden', thread.pinned && 'bg-selection text-fg')}>
        <Pin aria-hidden className="size-3" /> {thread.pinned ? 'Pinned' : 'Pin'}
      </button>
      {onDetails && (
        <button type="button" onClick={onDetails} aria-expanded={detailsOpen} className="inline-flex h-7 items-center gap-1.5 rounded-control border border-border px-2.5 text-label text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
          <PanelRight aria-hidden className="size-3.5" /> Details
        </button>
      )}
      {actions.length > 0 ? <OverflowMenu ariaLabel="Chat actions" actions={actions} align="end" /> : <MoreHorizontal aria-hidden className="size-4 text-fg-muted" />}
    </header>
  )
}
