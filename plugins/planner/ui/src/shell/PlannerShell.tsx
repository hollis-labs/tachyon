import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { EmptyState, Sheet, SheetContent, SheetTitle, Skeleton } from '@hollis-labs/design-components'
import { usePlannerApi, usePlannerEvents } from '@planner/api/context'
import { PlannerApiError as ApiError } from '@planner/api/errors'
import { notifyError } from '@planner/api/notifications'
import { Context } from '@planner/shell/context'
import { Focus } from '@planner/shell/focus'
import { Spine, rowsOf } from '@planner/shell/spine'
import { TEXT } from '@planner/lib/vocabulary'
import type { PlannerComment, PlannerDb, ThemeAction, ThemeBoard, ThemeRow } from '@planner/api/types'

const REFRESH_MS = 30_000
const PIN_KEY = 'planner.pins'

/** Pins are kept in this browser until the backend has a pin write (build pack section 7). */
function loadPins(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(PIN_KEY) ?? '[]') as string[])
  } catch {
    return new Set()
  }
}

/**
 * The Planner workbench: spine, focus, context. Themes lens first; the other
 * lenses still open their existing pages until their slice lands.
 */
export default function PlannerShell() {
  const api = usePlannerApi()
  const navigate = useNavigate()
  const { id } = useParams<{ id?: string }>()
  const [params, setParams] = useSearchParams()
  const { lastEvent } = usePlannerEvents()
  const [board, setBoard] = useState<ThemeBoard | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [off, setOff] = useState(false)
  const [pins, setPins] = useState(loadPins)
  const [needYouOnly, setNeedYouOnly] = useState(false)
  const [comments, setComments] = useState<PlannerComment[]>([])
  const [details, setDetails] = useState(false)
  const openId = id ? decodeURIComponent(id) : undefined
  const view = params.get('view') === 'timeline' ? 'timeline' : 'board'

  const load = useCallback(async () => {
    try {
      setBoard(await api.plannerThemeBoard({ include_proposed: true }))
      setError(null)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setOff(true)
      else setError(err instanceof Error ? err.message : TEXT.loadFailed)
    }
  }, [api])

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), REFRESH_MS)
    return () => clearInterval(t)
  }, [load, lastEvent])

  const row = useMemo(() => rowsOf(board).find((r) => r.id === openId), [board, openId])

  useEffect(() => {
    if (!row) return setComments([])
    let live = true
    api.plannerGet('themes', row.id).then((item) => live && setComments(item.comments ?? []), () => live && setComments([]))
    return () => { live = false }
  }, [api, row?.id, row?.rev]) // eslint-disable-line react-hooks/exhaustive-deps

  const open = (next: string) => navigate(`/planner/themes/${encodeURIComponent(next)}${view === 'timeline' ? '?view=timeline' : ''}`)
  const togglePin = (pid: string) =>
    setPins((p) => {
      const n = new Set(p)
      if (!n.delete(pid)) n.add(pid)
      localStorage.setItem(PIN_KEY, JSON.stringify([...n]))
      return n
    })
  const act = async (r: ThemeRow, action: ThemeAction, args?: { position?: number }) => {
    try {
      await api.plannerThemeAction(r.id, action, r.rev, args)
      await load()
    } catch (err) {
      await load()
      notifyError(err, TEXT.writeFailed)
    }
  }

  if (off) return <EmptyState variant="empty" title={TEXT.turnedOff} description={TEXT.turnedOffHint} />
  const selected = Boolean(row)
  return (
    <div className="flex h-full min-h-0 w-full bg-bg">
      <nav aria-label={TEXT.appName} className={`${selected ? 'hidden lg:flex' : 'flex'} min-h-0 w-full shrink-0 flex-col border-r border-border lg:w-72`}>
        {board ? (
          <Spine board={board} selectedId={openId} onSelect={open} onLens={(db: PlannerDb) => navigate(`/planner/${db}`)} pins={pins} onTogglePin={togglePin} needYouOnly={needYouOnly} onNeedYou={setNeedYouOnly} />
        ) : (
          <div className="flex flex-col gap-2 p-3" aria-busy>{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-10 w-full" />)}</div>
        )}
        {error && (
          <p role="alert" className="border-t border-border px-3 py-2 dash-caption text-danger">
            {error} <button type="button" className="underline" onClick={() => void load()}>{TEXT.retry}</button>
          </p>
        )}
      </nav>
      <main className={`${selected ? 'flex' : 'hidden lg:flex'} min-h-0 min-w-0 flex-1`}>
        {board && row ? (
          <>
            <Focus row={row} board={board} view={view} onView={(v) => setParams(v === 'timeline' ? { view: 'timeline' } : {})} onAction={act} onBack={() => navigate('/planner/themes')} onDetails={() => setDetails(true)} />
            <div className="hidden w-80 shrink-0 border-l border-border xl:flex xl:flex-col 2xl:w-96">
              <Context row={row} comments={comments} onCommented={() => void load()} />
            </div>
            <Sheet open={details} onOpenChange={setDetails}>
              <SheetContent className="xl:hidden">
                <SheetTitle className="sr-only">{TEXT.detailsLabel}</SheetTitle>
                <Context row={row} comments={comments} onCommented={() => void load()} />
              </SheetContent>
            </Sheet>
          </>
        ) : (
          <p className="m-auto dash-support text-fg-muted">{TEXT.selectItem}</p>
        )}
      </main>
    </div>
  )
}

