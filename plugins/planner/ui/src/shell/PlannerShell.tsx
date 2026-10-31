import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { EmptyState, Sheet, SheetContent, SheetTitle, Skeleton } from '@hollis-labs/design-components'
import { PlannerFrame } from '@planner/shell/frame'
import { usePlannerApi, usePlannerEvents } from '@planner/api/context'
import { PlannerApiError as ApiError } from '@planner/api/errors'
import { authorizing, notifyError, notifyWriteError } from '@planner/api/notifications'
import { PlannerCreateDialog } from '@planner/components/planner/planner-create-dialog'
import { CommandPalette, LinkLayer, PaletteHint, SpineList, type PaletteCommand } from '@planner/shared'
import { Context } from '@planner/shell/context'
import { LinkKindPicker } from '@planner/shell/link-picker'
import { usePlannerKeys } from '@planner/shell/use-planner-keys'
import { LENSES, LINK_KINDS, lensPath, type LinkKind } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { Focus } from '@planner/shell/focus'
import { themeRowActions } from '@planner/components/planner/theme-board'
import { needsYou, rowsOf, useThemeGroups } from '@planner/shell/spine'
import { LoadError, loadMessage } from '@planner/shell/load-error'
import { TEXT } from '@planner/lib/vocabulary'
import type { LinksView, PlannerComment, PlannerDb, ThemeAction, ThemeBoard, ThemeRow } from '@planner/api/types'
import { THEME_SECTIONS } from '@planner/lib/planner'

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
  // Counts the stream's signals so the open theme's board and timeline refetch with the spine.
  const [tick, setTick] = useState(0)
  useEffect(() => { if (lastEvent) setTick((n) => n + 1) }, [lastEvent])
  const [board, setBoard] = useState<ThemeBoard | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [off, setOff] = useState(false)
  const [pins, setPins] = useState(loadPins)
  const [needYouOnly, setNeedYouOnly] = useState(params.get('facet') === 'need-you')
  const [needYouIds, setNeedYouIds] = useState<ReadonlySet<string> | undefined>(undefined)
  const warnedNeedYou = useRef(false)
  const [needsAccess, setNeedsAccess] = useState(false)
  const [goodAt, setGoodAt] = useState<number | null>(null)
  const [comments, setComments] = useState<PlannerComment[]>([])
  const [details, setDetails] = useState(false)
  const [ctxCollapsed, setCtxCollapsed] = useState(false)
  const [counts, setCounts] = useState<Partial<Record<PlannerDb, number>>>({})
  const [paletteOpen, setPaletteOpen] = useState<'main' | 'link' | null>(null)
  const [creating, setCreating] = useState<'themes' | 'ideas' | null>(null)
  const [createTitle, setCreateTitle] = useState('')
  const [linksView, setLinksView] = useState<LinksView | null>(null)
  const [pendingLink, setPendingLink] = useState<{ from: string; to: string } | null>(null)
  const extraCommands = useContributions('planner.palette.command')
  const openId = id ? decodeURIComponent(id) : undefined
  const view = params.get('view') === 'timeline' ? 'timeline' : 'board'

  const load = useCallback(async () => {
    try {
      setBoard(await api.plannerThemeBoard({ include_proposed: true }))
      setGoodAt(Date.now())
      setError(null)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'feature_disabled') setOff(true)
      else setError(loadMessage(err))
    }
  }, [api])

  useEffect(() => {
    void load()
    // The server's answer decides who needs you; if it cannot answer, the spine keeps its local reading.
    api.plannerNeedYou().then((n) => setNeedYouIds(new Set(n.items.filter((i) => i.db === 'themes').map((i) => i.id))), () => { setNeedYouIds(undefined); if (!warnedNeedYou.current) { warnedNeedYou.current = true; console.info('planner: /need-you unavailable, using the local reading') } })
    api.plannerDatabases().then((d) => setCounts(Object.fromEntries(d.map((x) => [x.name, x.count]))), () => undefined)
    const t = setInterval(() => void load(), REFRESH_MS)
    return () => clearInterval(t)
  }, [load, lastEvent])

  const row = useMemo(() => rowsOf(board).find((r) => r.id === openId), [board, openId])

  // On a wide screen the focus pane is never left empty: with nothing open, open the first theme.
  // (Narrow screens keep the list, since opening a theme replaces it.)
  useEffect(() => {
    if (openId || !board || typeof window.matchMedia !== 'function' || !window.matchMedia('(min-width: 640px)').matches) return
    // The first theme that needs the operator, else the first in the list.
    const rows = rowsOf(board)
    const first = rows.find((r) => (needYouIds ? needYouIds.has(r.id) : needsYou(r))) ?? rows[0]
    if (first) navigate(`/planner/themes/${encodeURIComponent(first.id)}${view === 'timeline' ? '?view=timeline' : ''}`, { replace: true })
  }, [openId, board, needYouIds])

  useEffect(() => {
    if (!row) return setComments([])
    let live = true
    api.plannerGet('themes', row.id).then((item) => live && setComments(item.comments ?? []), () => live && setComments([]))
    return () => { live = false }
  }, [api, row?.id, row?.rev])

  useEffect(() => {
    if (!row) return setLinksView(null)
    let live = true
    api.plannerLinks('themes', row.id).then((v) => live && setLinksView(v), () => live && setLinksView(null))
    return () => { live = false }
  }, [api, row?.id, row?.rev])

  const open = (next: string) => navigate(`/planner/themes/${encodeURIComponent(next)}${view === 'timeline' ? '?view=timeline' : ''}`)
  // The server owns pins once the board reports them; until then they stay in this browser.
  const serverPins = useMemo(() => rowsOf(board).some((r) => r.pinned !== undefined), [board])
  const shownPins = useMemo(() => (serverPins ? new Set(rowsOf(board).filter((r) => r.pinned).map((r) => r.id)) : pins), [board, pins, serverPins])
  const togglePin = async (pid: string) => {
    const r = rowsOf(board).find((x) => x.id === pid)
    if (!serverPins || !r) {
      setPins((p) => {
        const n = new Set(p)
        if (!n.delete(pid)) n.add(pid)
        localStorage.setItem(PIN_KEY, JSON.stringify([...n]))
        return n
      })
      return
    }
    try {
      await authorizing(() => api.plannerPin('themes', pid, !r.pinned, r.rev))
    } catch (err) {
      notifyWriteError(err, TEXT.writeFailed, () => setNeedsAccess(true))
    }
    await load()
  }
  // Pins made in this browser before the server kept them are sent once, then forgotten.
  useEffect(() => {
    if (!serverPins || pins.size === 0) return
    const todo = rowsOf(board).filter((r) => pins.has(r.id) && !r.pinned)
    localStorage.removeItem(PIN_KEY)
    setPins(new Set())
    if (todo.length === 0) return
    toast(TEXT.pinsSynced)
    void Promise.allSettled(todo.map((r) => api.plannerPin('themes', r.id, true, r.rev))).then(() => load())
  }, [serverPins])
  const act = async (r: ThemeRow, action: ThemeAction, args?: { position?: number }) => {
    try {
      await authorizing(() => api.plannerThemeAction(r.id, action, r.rev, args))
      await load()
    } catch (err) {
      await load()
      notifyWriteError(err, TEXT.writeFailed, () => setNeedsAccess(true))
    }
  }

  const edit = async (r: ThemeRow, patch: Record<string, unknown>) => {
    try {
      await authorizing(() => api.plannerUpdate('themes', r.id, patch, r.rev))
    } catch (err) {
      notifyWriteError(err, TEXT.writeFailed, () => setNeedsAccess(true))
    }
    await load()
  }

  const all = useMemo(() => rowsOf(board), [board])
  const titleOf = (rid: string) => all.find((r) => r.id === rid)?.title ?? rid
  const kindLabel = (k: string) => LINK_KINDS.find((x) => x.id === k)?.label ?? k
  // The spine tags the two themes of a link just made, for a few seconds (the golden's "Link themes" state); at rest it tags nothing.
  const [justLinked, setJustLinked] = useState<{ from: string; to: string; kind: string } | null>(null)
  const spineLinks = useMemo(() => new Map(justLinked ? [[justLinked.from, { kind: justLinked.kind, other: justLinked.to }], [justLinked.to, { kind: justLinked.kind, other: justLinked.from }]] : []), [justLinked])
  useEffect(() => {
    if (!justLinked) return
    const t = setTimeout(() => setJustLinked(null), 6000)
    return () => clearTimeout(t)
  }, [justLinked])
  const rowLinks = [
    ...(linksView?.outbound ?? []).map((l) => ({ kind: l.kind, label: l.title, note: l.status })),
    ...(linksView?.inbound ?? []).map((l) => ({ kind: l.kind, label: l.title, note: TEXT.linkedFromNote })),
    ...(linksView?.external ?? []).map((l) => ({ kind: l.kind, label: l.label })),
  ]
  const addLink = async (kind: LinkKind) => {
    if (!pendingLink) return
    const { from, to } = pendingLink
    const source = all.find((r) => r.id === from)
    setPendingLink(null)
    if (!source) return
    try {
      const done = await authorizing(() => api.plannerRelationAdd('themes', from, kind, { db: 'themes', id: to }, source.rev))
      setLinksView(done.links_view)
      setJustLinked({ from, to, kind: kindLabel(kind) })
      toast(TEXT.linkAdded(titleOf(from), kindLabel(kind), titleOf(to)), {
        duration: 8000,
        action: {
          label: TEXT.undo,
          onClick: () => api.plannerRelationRemove('themes', from, kind, { db: 'themes', id: to }, (done.item.rev ?? done.item.revision ?? source.rev)).then((r) => setLinksView(r.links_view), (err) => notifyError(err, TEXT.writeFailed)),
        },
      })
      await load()
    } catch (err) {
      if (err instanceof ApiError && err.code === 'cycle') toast(TEXT.linkCycle)
      else notifyError(err, TEXT.writeFailed)
      await load()
    }
  }
  const step = (delta: 1 | -1) => {
    const rows = [...document.querySelectorAll<HTMLElement>('[role="listbox"] [role="option"]')]
    const at = rows.findIndex((r) => r.getAttribute('aria-selected') === 'true')
    const next = rows[Math.max(0, Math.min(rows.length - 1, at + delta))]
    next?.click()
    next?.focus()
  }
  usePlannerKeys(
    {
      palette: () => setPaletteOpen('main'),
      create: () => setCreating('themes'),
      link: () => row && setPaletteOpen('link'),
      lens: (k) => {
        if (k === 'n') return setNeedYouOnly(true)
        const lens = LENSES.find((l) => l.key === k)
        if (lens && lens.db !== 'themes') navigate(lensPath(lens.db))
      },
      view: (v) => row && setParams(v === 'timeline' ? { view: 'timeline' } : {}),
      pin: () => row && void togglePin(row.id),
      finish: () => row && void act(row, 'finish'),
      step,
      clearFilter: () => (needYouOnly ? (setNeedYouOnly(false), true) : false),
    },
    !off,
  )
  const commands = useCallback(
    (q: string): PaletteCommand[] => [
      { id: 'create:themes', mode: 'create', text: TEXT.createTheme, label: q ? TEXT.createThemeIn(q) : TEXT.createTheme, group: TEXT.paletteKindTheme, hint: '↵', always: true },
      { id: 'create:ideas', mode: 'create', text: TEXT.createIdea, label: q ? TEXT.createIdeaNamed(q) : TEXT.createIdea, group: TEXT.paletteKindIdea, hint: 'I', always: true },
      ...extraCommands.map((c) => ({ id: `ext:${c.id}`, mode: String((c.data as { mode?: string } | undefined)?.mode ?? 'create'), text: c.label ?? c.id, label: c.label ?? c.id })),
      ...all.map((r) => ({ id: `open:${r.id}`, mode: 'open', group: TEXT.paletteKindTheme, hint: row && r.id !== row.id ? TEXT.paletteLink : undefined, text: `${r.id} ${r.title}`, label: r.title })),
    ],
    [all, extraCommands, row],
  )
  const linkCommands = useMemo<PaletteCommand[]>(() => all.filter((r) => r.id !== openId).map((r) => ({ id: r.id, mode: 'link', text: `${r.id} ${r.title}`, label: <span><span className="mr-2 font-mono dash-caption text-fg-faint">{r.id}</span>{r.title}</span> })), [all, openId])

  const needs = useCallback((r: ThemeRow) => (needYouIds ? needYouIds.has(r.id) : needsYou(r)), [needYouIds])
  const itemMenu = useContributions('planner.spine.item.menu')
  const lensExtras = useContributions('planner.lens')
  const groups = useThemeGroups({ board, pins: shownPins, needYouOnly, needs, links: spineLinks, itemMenu })
  const actions = useMemo(() => (row ? themeRowActions(row.section, row.rank, (board?.totals.in_flight ?? 0) + (board?.totals.cleared ?? 0)) : []), [row, board])
  if (off) return <EmptyState variant="empty" title={TEXT.turnedOff} description={TEXT.turnedOffHint} />
  const selected = Boolean(row)
  const context = row && (
    <Context
      db="themes"
      id={row.id}
      rev={row.rev}
      pinned={row.pinned ?? false}
      comments={comments}
      links={rowLinks}
      decisions={row.pending_decisions}
      onCommented={() => void load()}
      mentionable={all.map((r) => ({ id: r.id, title: r.title, ref: `#${r.rank}` }))}
      statusOptions={actions.map((a) => ({ value: a.label, label: a.label }))}
      onStatus={async (label) => { const a = actions.find((x) => x.label === label); if (a) await act(row, a.action, a.position === undefined ? undefined : { position: a.position }) }}
    />
  )
  const spineBody = board ? (
    <SpineList label={TEXT.spineLabel} groups={groups} selectedId={openId} onSelect={open} onTogglePin={(pid) => void togglePin(pid)} pendingLabel={TEXT.saving} pinnedLabel={TEXT.pinned} emptyLabel={needYouOnly ? TEXT.needYouEmpty : TEXT.lensEmpty('Themes')} />
  ) : error ? (
    <LoadError title={TEXT.loadFailedLens('Themes')} message={error} onRetry={() => void load()} />
  ) : (
    <div role="status" aria-busy aria-label={TEXT.loading} className="flex flex-col gap-2 p-1">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-10 w-full" />)}</div>
  )
  const notice = error ? (
    board ? (
      <p role="status" className="border-t border-border-subtle px-3 py-2 text-caption text-fg-muted">
        {TEXT.reconnecting(new Date(goodAt ?? Date.now()).toLocaleTimeString())}. {TEXT.writesPaused} <button type="button" className="underline" onClick={() => void load()}>{TEXT.retry}</button>
      </p>
    ) : null
  ) : null
  return (
    <LinkLayer titleOf={titleOf} labels={{ port: TEXT.linkPort, dragging: TEXT.linkDragging }} onLink={(from, to) => setPendingLink({ from, to })}>
      <PlannerFrame
        active="themes"
        onLens={(db: PlannerDb) => navigate(lensPath(db))}
        counts={counts}
        portfolio={board?.progress}
        needYou={{ count: all.filter(needs).length, active: needYouOnly, onToggle: () => setNeedYouOnly((v) => !v) }}
        freshAt={goodAt}
        stale={Boolean(error)}
        onPalette={() => setPaletteOpen('main')}
        onNew={() => setCreating('themes')}
        onDetails={() => setDetails(true)}
        contextCollapsed={ctxCollapsed}
        onToggleContext={() => setCtxCollapsed((v) => !v)}
        focused={selected}
        onBack={() => navigate('/planner/themes')}
        spine={<div className={error ? 'opacity-60' : undefined}>{spineBody}</div>}
        spineNotice={notice}
        context={context}
        extraLenses={lensExtras.map((c) => ({ value: c.id, label: c.label ?? c.id }))}
      >
        {board && row ? (
          <Focus tick={tick} paused={Boolean(error)} needsAccess={needsAccess} onRetryAccess={() => setNeedsAccess(false)} onNeedsAccess={() => setNeedsAccess(true)} row={row} board={board} view={view} onView={(v) => setParams(v === 'timeline' ? { view: 'timeline' } : {})} onAction={act} pinned={shownPins.has(row.id)} onTogglePin={() => void togglePin(row.id)} onEdit={edit} onLink={() => setPaletteOpen('link')} />
        ) : (
          <p className="m-auto text-xs text-fg-muted">{TEXT.selectItem}</p>
        )}
      </PlannerFrame>
      <Sheet open={details && selected} onOpenChange={setDetails}>
        <SheetContent className="lg:hidden max-sm:w-full! max-sm:max-w-none!">
          <SheetTitle className="sr-only">{TEXT.detailsLabel}</SheetTitle>
          {context}
        </SheetContent>
      </Sheet>
      <CommandPalette
        key="main"
        open={paletteOpen === 'main'}
        onOpenChange={(o) => !o && setPaletteOpen(null)}
        title={TEXT.paletteTitle}
        modes={[{ id: 'create', label: TEXT.modeCreate }, { id: 'open', label: TEXT.modeOpen }]}
        commands={commands}
        emptyLabel={TEXT.paletteEmpty}
        hints={<><PaletteHint k="↵">{TEXT.hintEnter}</PaletteHint>{row && <PaletteHint k="⇥">{TEXT.hintTab(`#${row.rank}`)}</PaletteHint>}<PaletteHint k="@">{TEXT.hintAt}</PaletteHint><PaletteHint k="esc">{TEXT.hintEsc}</PaletteHint></>}
        onRun={(cmd, q) => {
          if (cmd.startsWith('open:')) open(cmd.slice(5))
          else if (cmd === 'create:themes' || cmd === 'create:ideas') (setCreateTitle(q), setCreating(cmd === 'create:ideas' ? 'ideas' : 'themes'))
        }}
        onRunAndLink={row ? (cmd) => cmd.startsWith('open:') && setPendingLink({ from: row.id, to: cmd.slice(5) }) : undefined}
      />
      <CommandPalette key="link" open={paletteOpen === 'link'} onOpenChange={(o) => !o && setPaletteOpen(null)} title={TEXT.linkTo} modes={[{ id: 'link', label: TEXT.linkTo }]} commands={linkCommands} emptyLabel={TEXT.paletteEmpty} onRun={(to) => row && setPendingLink({ from: row.id, to })} />
      <LinkKindPicker open={pendingLink !== null} from={pendingLink ? titleOf(pendingLink.from) : ''} to={pendingLink ? titleOf(pendingLink.to) : ''} onPick={(k) => void addLink(k)} onCancel={() => setPendingLink(null)} />
      <PlannerCreateDialog db={creating ?? 'themes'} initialTitle={createTitle} open={creating !== null} onOpenChange={(o) => !o && setCreating(null)} nextRank={(board?.totals.in_flight ?? 0) + (board?.totals.cleared ?? 0) + 1} onCreated={(it) => { void load(); if (creating === 'themes') open(it.id) }} />
    </LinkLayer>
  )
}

