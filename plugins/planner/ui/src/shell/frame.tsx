import { useEffect, useState, type ReactNode } from 'react'
import { PanelRight, Plus } from 'lucide-react'
import { Button, cn, Tabs, TabsList, TabsTrigger } from '@hollis-labs/design-components'
import { CAPTION, Kbd, StateDot } from '@planner/shared'
import { LENSES, TEXT } from '@planner/lib/vocabulary'
import type { PlannerDb } from '@planner/api/types'

const compact = new Intl.NumberFormat('en-US')

/** "live · 12s" under a minute, then minutes; stale data says so instead. */
function useFreshness(at: number | null, stale: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])
  if (at === null) return ''
  if (stale) return TEXT.stale(new Date(at).toLocaleTimeString())
  const s = Math.max(0, Math.round((now - at) / 1000))
  return TEXT.live(s < 60 ? `${s}s` : `${Math.floor(s / 60)}m`)
}

export interface PlannerFrameProps {
  active: PlannerDb
  onLens: (db: PlannerDb) => void
  /** Item count per lens, shown beside each lens name. */
  counts?: Partial<Record<PlannerDb, number>>
  /** The whole portfolio's tasks, done of total. */
  portfolio?: { done: number; total: number }
  needYou: { count: number; active: boolean; onToggle: () => void }
  /** When the last good read happened, and whether the latest one failed. */
  freshAt: number | null
  stale?: boolean
  onPalette: () => void
  onNew: () => void
  /** Details open as a drawer below the wide breakpoint. */
  onDetails?: () => void
  contextCollapsed: boolean
  onToggleContext: () => void
  /** A focus pane is open: at phone width it replaces the spine. */
  focused: boolean
  onBack: () => void
  spine: ReactNode
  /** Under the spine list: reconnect or error notices. */
  spineNotice?: ReactNode
  children: ReactNode
  context?: ReactNode
  extraLenses?: readonly { value: string; label: string }[]
}

/**
 * The Planner workbench frame every lens shares: header, spine (portfolio
 * progress, lens switcher, Need-you row, list), focus pane, collapsible
 * context column. Regions follow the flow spec; nothing here knows a lens.
 */
export function PlannerFrame({ active, onLens, counts, portfolio, needYou, freshAt, stale, onPalette, onNew, onDetails, contextCollapsed, onToggleContext, focused, onBack, spine, spineNotice, children, context, extraLenses = [] }: PlannerFrameProps) {
  const fresh = useFreshness(freshAt, Boolean(stale))
  const pct = portfolio && portfolio.total > 0 ? Math.round((portfolio.done / portfolio.total) * 100) : null
  return (
    <div className="flex h-full min-h-0 w-full flex-col bg-bg text-fg">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border-subtle px-4">
        {focused && (
          <Button variant="outline" size="sm" onClick={onBack} className="sm:hidden">‹ {TEXT.back}</Button>
        )}
        <h1 className="font-mono text-caption font-semibold uppercase tracking-label max-sm:hidden">{TEXT.appName}</h1>
        <Button variant="outline" size="sm" onClick={onPalette} className="min-w-0 justify-start font-normal text-fg-muted max-sm:flex-1 sm:w-80">
          <span className="truncate">{TEXT.searchPlaceholder}</span>
          <Kbd className="ml-auto">/</Kbd>
        </Button>
        <span className="flex-1" />
        <span className="text-label text-fg-muted max-sm:hidden">{fresh}</span>
        {onDetails && focused && (
          <Button variant="outline" size="sm" onClick={onDetails} className="lg:hidden">{TEXT.detailsLabel}</Button>
        )}
        <Button variant="outline" size="icon-sm" aria-label={contextCollapsed ? TEXT.showDetails : TEXT.collapseDetails} aria-pressed={contextCollapsed} onClick={onToggleContext} className="max-lg:hidden">
          <PanelRight />
        </Button>
        <Button size="sm" onClick={onNew} className="font-semibold">
          <Plus /> {TEXT.newLabel} <Kbd className="border-primary-fg/30 text-primary-fg max-sm:hidden">N</Kbd>
        </Button>
      </header>
      <div className="flex min-h-0 flex-1">
        <section aria-label={TEXT.spineLabel} className={cn('min-h-0 shrink-0 flex-col border-r border-border-subtle max-sm:w-full max-sm:border-r-0 sm:w-65 xl:w-74', focused ? 'hidden sm:flex' : 'flex')}>
          <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border-subtle px-3">
            <span className={CAPTION}>{TEXT.appName}</span>
            <span className="flex-1" />
            {portfolio && portfolio.total > 0 && (
              <span className="text-label tabular-nums text-fg-muted" aria-label={TEXT.portfolioLabel(portfolio.done, portfolio.total)}>
                {compact.format(portfolio.done)} / {compact.format(portfolio.total)} · {pct}%
              </span>
            )}
          </div>
          <nav aria-label={TEXT.lensesLabel} className="shrink-0 px-2 pb-1 pt-2">
            <Tabs value={active} onValueChange={(v) => LENSES.some((l) => l.db === v) && onLens(v as PlannerDb)} className="gap-0">
              <TabsList variant="line" className="w-full flex-wrap justify-start gap-1 p-0 group-data-horizontal/tabs:h-auto max-sm:flex-nowrap max-sm:overflow-x-auto">
                {LENSES.map((l) => (
                  <TabsTrigger key={l.db} value={l.db} className="h-5.5 flex-none rounded-full border-transparent px-2 text-label font-normal text-fg-muted after:hidden hover:text-fg data-active:border-border! data-active:bg-selection! data-active:text-fg!">
                    {l.label}
                    {counts?.[l.db] !== undefined && <b className="font-mono text-caption font-normal tabular-nums text-fg-faint">{counts[l.db]}</b>}
                  </TabsTrigger>
                ))}
                {extraLenses.map((l) => (
                  <TabsTrigger key={l.value} value={l.value} className="h-5.5 flex-none rounded-full border-transparent px-2 text-label font-normal text-fg-muted after:hidden">{l.label}</TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          </nav>
          <div className="px-2 pt-1">
            <button
              type="button"
              aria-pressed={needYou.active}
              onClick={needYou.onToggle}
              className={cn('flex h-7 w-full items-center gap-2 rounded-control border border-warning/30 px-2.5 text-xs text-warning hover:bg-selection', needYou.active && 'bg-selection')}
            >
              <StateDot state="review" />
              {TEXT.needYouCount(needYou.count)}
              <Kbd className="ml-auto">G N</Kbd>
            </button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2 pt-1">{spine}</div>
          {spineNotice}
        </section>
        <main className={cn('min-h-0 min-w-0 flex-1 flex-col', focused ? 'flex' : 'hidden sm:flex')}>{children}</main>
        {context && !contextCollapsed && (
          <aside aria-label={TEXT.detailsLabel} className="hidden min-h-0 w-85 shrink-0 flex-col border-l border-border-subtle bg-bg-elevated lg:flex">{context}</aside>
        )}
      </div>
    </div>
  )
}
