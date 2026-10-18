import { dataRefreshRegistry } from "@tachyon/host-sdk"
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Plus, X } from 'lucide-react'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { Button, EmptyState, Skeleton } from '@hollis-labs/design-components'
import { PageHeader, statusTone } from '@hollis-labs/kit-dashboard'
import { DashboardSearchToolbar, DashboardToolbar, DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { FilterChipGroup } from '@hollis-labs/kit-dashboard/data'
import { PlannerBoardView } from '@planner/components/planner/planner-board'
import { PlannerCreateDialog } from '@planner/components/planner/planner-create-dialog'
import { PlannerDetailDialog } from '@planner/components/planner/planner-detail-dialog'
import { PlannerRow } from '@planner/components/planner/planner-row'
import { PlannerRowContent, PlannerTableHeader } from '@planner/components/planner/planner-table'
import { TorqueTaskDialog } from '@planner/components/planner/torque-task-dialog'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import { usePlannerRowFocus } from '@planner/hooks/use-planner-row-focus'
import { useApi } from '@tachyon/torque-client'
import { useSSE } from '@tachyon/torque-client'
import { ApiError } from '@tachyon/torque-client'
import { PLANNER_DBS, filterItems, isPlannerDb, isSettled, moveId, parseBoardOpen, plannerMeta, statusCounts, statusToneKey } from '@planner/lib/planner'
import { notifyError } from '@tachyon/torque-client'
import type { PlannerBoard, PlannerDatabaseInfo, PlannerDb, PlannerItem, PlannerSearchHit } from '@tachyon/torque-client'

const SSE_EVENTS = ['planner.changed', 'task.created', 'task.updated', 'task.transitioned']
/** The board is a live view: it also refreshes on its own while open. */
const BOARD_REFRESH_MS = 30_000

const VIEW_ACCENTS = { board: 'doing', activity: 'done', priorities: 'queued', workstreams: 'doing', roadmap: 'done', ideas: 'review', decisions: 'review', risks: 'blocked', inbox: 'queued' } as const

export default function PlannerPage() {
  const rowFocus = usePlannerRowFocus()
  const api = useApi()
  const reordering = useRef(false)
  const itemReadVersion = useRef(0)
  const navigate = useNavigate()
  const params = useParams<{ db?: string; id?: string }>()
  const [searchParams, setSearchParams] = useSearchParams()
  // /planner and /planner/board are the curated board, /planner/activity the
  // activity feed (scope=all); /planner/<db> is a list.
  const isActivity = params.db === 'activity'
  // Torque's agents / agents-demo / builder views live in the Console plugin
  // (its own /agents and /builder nav items), so they have no tab here.
  const isDemo = false
  const isBoard = params.db === undefined || params.db === 'board' || isActivity
  const boardScope = isActivity ? 'all' : 'curated'
  const boardBase = isActivity ? '/planner/activity' : '/planner/board'
  const db: PlannerDb = isPlannerDb(params.db) ? params.db : 'priorities'
  const openId = params.id ? decodeURIComponent(params.id) : undefined
  const boardOpen = isBoard ? parseBoardOpen(searchParams) : {} // the board opens items in modals over itself
  const meta = plannerMeta(db)
  const createDb: PlannerDb = isBoard ? 'inbox' : db // the board's New button adds to the inbox
  const createMeta = plannerMeta(createDb)

  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [counts, setCounts] = useState<PlannerDatabaseInfo[]>([])
  const [board, setBoard] = useState<PlannerBoard | null>(null)
  const [boardError, setBoardError] = useState<string | null>(null)
  const [items, setItems] = useState<PlannerItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState('')
  const [query, setQuery] = useState('')
  const [showSettled, setShowSettled] = useState(false)
  const [search, setSearch] = useState('')
  const [hits, setHits] = useState<PlannerSearchHit[] | null>(null)
  const [creating, setCreating] = useState(false)
  const [refreshKey, setRefreshKey] = useState(0)
  const { lastEvent } = useSSE(SSE_EVENTS)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }))

  const loadCounts = useCallback(async () => {
    try {
      setCounts(await api.plannerDatabases())
      setEnabled(true)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setEnabled(false)
      else setError(err instanceof Error ? err.message : 'Failed to load the planner')
    }
  }, [api])

  const loadItems = useCallback(async () => {
    if (reordering.current) return
    const version = ++itemReadVersion.current
    try {
      const nextItems = await api.plannerList(db)
      if (version !== itemReadVersion.current || reordering.current) return
      setItems(nextItems)
      setError(null)
    } catch (err) {
      if (version !== itemReadVersion.current || reordering.current) return
      if (err instanceof ApiError && err.code === 'feature_disabled') setEnabled(false)
      else setError(err instanceof Error ? err.message : 'Failed to load')
    }
  }, [api, db])

  const loadBoard = useCallback(async () => {
    try {
      setBoard(await api.plannerBoard({ scope: boardScope }))
      setBoardError(null)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setEnabled(false)
      else setBoardError(err instanceof Error ? err.message : 'Failed to load the board')
    }
  }, [api, boardScope])

  useEffect(() => {
    void loadCounts()
  }, [loadCounts])

  // The board loads when opened and then keeps itself fresh.
  useEffect(() => {
    if (!isBoard) return
    setBoard(null) // the curated board and the activity feed are different documents
    void loadBoard()
    const t = setInterval(() => void loadBoard(), BOARD_REFRESH_MS)
    return () => clearInterval(t)
  }, [isBoard, loadBoard])

  // Switching database starts a fresh view.
  useEffect(() => {
    setItems(null)
    setStatus('')
    setQuery('')
    setShowSettled(false)
    setError(null)
    if (!isBoard && !isDemo) void loadItems()
  }, [loadItems, isBoard, isDemo])

  // The server says something changed: reload the list, the counts and any open item.
  useEffect(() => {
    if (!lastEvent) return
    if (isBoard) void loadBoard()
    if (lastEvent.type.startsWith('task.')) {
      setRefreshKey((k) => k + 1) // an open workstream or reference may show this task
      return
    }
    void loadCounts()
    if (!isBoard && !isDemo) void loadItems()
    setRefreshKey((k) => k + 1)
  }, [lastEvent])

  useEffect(() => dataRefreshRegistry('planner').subscribe(() => {
    void loadCounts()
    if (isBoard) void loadBoard()
    else if (!isDemo) void loadItems()
    setRefreshKey(k => k + 1)
  }), [isBoard, isDemo, loadBoard, loadCounts, loadItems])

  // Cross-database search, after a short pause in typing.
  useEffect(() => {
    const q = search.trim()
    if (q.length < 2) {
      setHits(null)
      return
    }
    const ctl = new AbortController()
    const t = setTimeout(() => {
      api.plannerSearch(q, undefined, ctl.signal).then(setHits).catch((err: unknown) => {
        if (!ctl.signal.aborted) notifyError(err, 'Search failed')
      })
    }, 250)
    return () => {
      clearTimeout(t)
      ctl.abort()
    }
  }, [api, search])

  const settledCount = useMemo(() => (items ?? []).filter((i) => isSettled(i.status)).length, [items])
  const statuses = useMemo(() => statusCounts(items ?? []), [items])
  const visible = useMemo(() => {
    const base = (items ?? []).filter((i) => status || showSettled || !isSettled(i.status))
    return filterItems(base, { status, query })
  }, [items, status, query, showSettled])
  const canReorder = !!items && visible.length === items.length && !status && !query.trim()

  const go = (nextDb: PlannerDb, id?: string) => navigate(id ? `/planner/${nextDb}/${encodeURIComponent(id)}` : `/planner/${nextDb}`)

  async function onDragEnd(e: DragEndEvent) {
    if (reordering.current || !items || !e.over || e.active.id === e.over.id) return
    const previous = items
    reordering.current = true
    itemReadVersion.current++
    const ids = moveId(items.map((i) => i.id), String(e.active.id), String(e.over.id))
    const byId = new Map(items.map((i) => [i.id, i]))
    setItems(ids.map((id) => byId.get(id)!)) // optimistic
    try {
      await api.plannerReorder(db, ids)
    } catch (err) {
      setItems(previous)
      notifyError(err, 'Failed to reorder')
    } finally {
      reordering.current = false
      void loadItems()
    }
  }

  async function enable() {
    try {
      await api.setSetting('features.planner', 'true')
      setEnabled(true)
      setError(null)
      void loadCounts()
      if (isBoard) void loadBoard()
      else void loadItems()
    } catch (err) {
      notifyError(err, 'Failed to turn on the planner')
    }
  }

  if (enabled === false) {
    return (
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Planner" />
        <div className="p-6">
          <EmptyState
            variant="empty"
            title="The planner is switched off"
            description="The planner holds priorities, workstreams, the roadmap, ideas, decisions, risks and an inbox. Turn it on to use it."
            action={{ label: 'Turn on planner', onClick: enable }}
          />
        </div>
      </div>
    )
  }

  const countOf = (key: PlannerDb) => counts.find((c) => c.name === key)?.count
  const maxRank = Math.max(0, ...(db === 'priorities' ? (items ?? []).map((i) => (typeof i.rank === 'number' ? i.rank : 0)) : [0]))

  return (
    <div className="planner-page flex h-full min-w-0 flex-col" data-row-focus={rowFocus}>
      <PageHeader title="Planner">
        {!isDemo && <Button variant="outline" size="sm" className="gap-1 dash-header-action" onClick={() => setCreating(true)} disabled={enabled === null}>
          <Plus className="h-3.5 w-3.5" />
          New {createMeta.singular}
        </Button>}
      </PageHeader>

      <DashboardSearchToolbar value={search} onChange={setSearch} label="Search the planner" placeholder="Search the planner…" slashToFocus layeredEscape retainFocusOnClear />
      <DashboardViewTabs label="Planner views" value={isActivity ? 'activity' : isBoard ? 'board' : db}
        onChange={value => { setSearch(''); navigate(`/planner/${value}`) }}
        items={[
          { value: 'board', label: 'Board', tone: VIEW_ACCENTS.board },
          { value: 'activity', label: 'Activity', tone: VIEW_ACCENTS.activity },
          ...PLANNER_DBS.map(d => ({ value: d.key, label: d.label, count: countOf(d.key), tone: VIEW_ACCENTS[d.key] })),
        ]} />

      <div className="flex-1 overflow-auto">
        {hits !== null ? (
          <SearchResults hits={hits} query={search.trim()} onClear={() => setSearch('')} onOpen={(h) => { setSearch(''); go(h.db, h.id) }} />
        ) : isBoard ? (
          boardError ? (
            <div className="p-4">
              <EmptyState variant="error" title="Something went wrong" description={boardError} action={{ label: 'Retry', onClick: () => void loadBoard() }} />
            </div>
          ) : board === null ? (
            <div className="flex flex-col divide-y divide-border">
              <Skeleton className="h-40 w-full" />
              <Skeleton className="h-40 w-full" />
              <Skeleton className="h-40 w-full" />
              <Skeleton className="h-40 w-full" />
            </div>
          ) : (
            <PlannerBoardView board={board} scope={boardScope} base={boardBase} />
          )
        ) : (
          <>
            <div className="dash-record-section-header">
              <h2 className={`font-mono dash-caption font-medium uppercase tracking-label ${statusTone(VIEW_ACCENTS[db]).text}`}>{meta.label}</h2>
              {items && <span className="font-mono dash-caption tabular-nums text-fg-faint">{visible.length} shown · {items.length} total</span>}
              <p className="dash-support leading-4 text-fg-muted">{meta.blurb}</p>
            </div>
            <DashboardSearchToolbar value={query} onChange={setQuery} label={`Filter ${meta.label}`} placeholder={`Filter ${meta.label.toLowerCase()}…`}>
              <FilterChipGroup ariaLabel="Filter by status" selected={[status]} onToggle={value => setStatus(status === value ? '' : value)}
                chips={[
                  { value: '', label: 'All' },
                  ...statuses.map(s => {
                    const tone = statusTone(statusToneKey(s.status))
                    return { value: s.status, label: <><span className={`size-1.5 rounded-full ${tone.dot}`} />{s.status} <span className="font-mono tabular-nums opacity-70">{s.count}</span></>, activeClassName: `${tone.border} ${tone.bg} ${tone.text}` }
                  }),
                ]} />
              {settledCount > 0 && !status && (
                <Button variant="ghost" size="sm" aria-pressed={showSettled} className="ml-auto dash-label text-info" onClick={() => setShowSettled(v => !v)}>
                  {showSettled ? `Hide ${settledCount} settled` : `Show ${settledCount} settled`}
                </Button>
              )}
            </DashboardSearchToolbar>

            {error ? (
              <div className="p-4">
                <EmptyState variant="error" title="Something went wrong" description={error} action={{ label: 'Retry', onClick: () => void loadItems() }} />
              </div>
            ) : items === null ? (
              <div className="flex flex-col gap-2 p-4">
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
              </div>
            ) : items.length === 0 ? (
              <div className="p-4">
                <EmptyState variant="empty" title={`No ${meta.label.toLowerCase()} yet`} description={meta.blurb} action={{ label: `New ${meta.singular}`, onClick: () => setCreating(true) }} />
              </div>
            ) : visible.length === 0 ? (
              <p className="px-4 py-4 dash-support text-fg-muted">Nothing matches. Clear the filter or show settled items.</p>
            ) : (
              <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
                <SortableContext items={visible.map((i) => i.id)} strategy={verticalListSortingStrategy} disabled={!canReorder}>
                  <PlannerTableHeader />
                  <ul aria-label={meta.label}>
                    {visible.map((it) => (
                      <PlannerRow key={it.id} db={db} item={it} sortable={canReorder} onOpen={() => go(db, it.id)} />
                    ))}
                  </ul>
                </SortableContext>
              </DndContext>
            )}
            {items && items.length > 1 && !canReorder && !error && (
              <p className="px-4 py-2 font-mono dash-caption leading-4 text-fg-faint">Clear the filter and show settled items to drag-reorder.</p>
            )}
          </>
        )}
      </div>

      <PlannerCreateDialog
        db={createDb}
        open={creating}
        onOpenChange={setCreating}
        nextRank={maxRank + 1}
        onCreated={(it) => {
          void loadCounts()
          if (isBoard) void loadBoard()
          else void loadItems()
          go(createDb, it.id)
        }}
      />

      {boardOpen.item && (
        <PlannerDetailDialog
          key={`board/${boardOpen.item.db}/${boardOpen.item.id}`}
          db={boardOpen.item.db}
          id={boardOpen.item.id}
          refreshKey={refreshKey}
          onClose={() => setSearchParams({})}
          onChanged={() => {
            void loadCounts()
            void loadBoard()
          }}
          onOpen={(nextDb, id) => navigate(`${boardBase}?open=${nextDb}/${encodeURIComponent(id)}`)}
        />
      )}

      {boardOpen.task && !boardOpen.item && <TorqueTaskDialog key={boardOpen.task} id={boardOpen.task} onClose={() => setSearchParams({})} />}

      {openId && !isBoard && (
        <PlannerDetailDialog
          key={`${db}/${openId}`}
          db={db}
          id={openId}
          refreshKey={refreshKey}
          onClose={() => go(db)}
          onChanged={() => {
            void loadCounts()
            void loadItems()
          }}
          onOpen={(nextDb, id) => go(nextDb, id)}
        />
      )}
    </div>
  )
}

function SearchResults({ hits, query, onClear, onOpen }: { hits: PlannerSearchHit[]; query: string; onClear: () => void; onOpen: (h: PlannerSearchHit) => void }) {
  return (
    <section aria-label="Search results">
      <DashboardToolbar className="justify-between border-b border-border bg-panel-2/30 px-4 py-2">
        <p className="dash-support text-fg-muted">
          {hits.length} result{hits.length === 1 ? '' : 's'} for <span className="font-medium">“{query}”</span>
        </p>
        <Button size="sm" variant="ghost" className="gap-1" onClick={onClear}>
          <X className="h-3.5 w-3.5" /> Clear
        </Button>
      </DashboardToolbar>
      {hits.length === 0 ? (
        <p className="px-4 py-4 dash-support text-fg-muted">Nothing matched. Every word has to appear in an item&apos;s id, title or text.</p>
      ) : (
        <>
        <PlannerTableHeader trailing="" />
        <ul>
          {hits.map((h) => (
            <li key={`${h.db}/${h.id}`} className="dash-record-list-row" >
              <button type="button" className="planner-row-trigger dash-record-grid" onClick={() => onOpen(h)} aria-label={`Open ${h.id}: ${h.title}`}>
                <PlannerRowContent title={h.title} metadata={<><span className="shrink-0">{h.id}</span><span className={statusTone(VIEW_ACCENTS[h.db]).text}>{plannerMeta(h.db).label}</span></>} status={<PlannerStatus status={h.status} />} />
              </button>
            </li>
          ))}
        </ul>
        </>
      )}
    </section>
  )
}
