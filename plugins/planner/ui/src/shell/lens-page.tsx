import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ChevronDown, Pin } from 'lucide-react'
import { OverflowMenu, Sheet, SheetContent, SheetTitle, Skeleton } from '@hollis-labs/design-components'
import { createPlannerId } from '@planner/api/identity'
import { usePlannerApi, usePlannerEvents } from '@planner/api/context'
import { notifyError } from '@planner/api/notifications'
import { PlannerCreateDialog } from '@planner/components/planner/planner-create-dialog'
import { usePlannerSchema } from '@planner/hooks/use-planner-schema'
import { boardAge, sentenceCase } from '@planner/lib/planner'
import { LENSES, lensPath, TEXT } from '@planner/lib/vocabulary'
import { CAPTION, Chip, CommandPalette, InlineEdit, PaletteHint, Pips, SpineList, StateDot, StatusMenu, type DotState, type PaletteCommand, type SpineGroup } from '@planner/shared'
import { Context, type ContextLink } from '@planner/shell/context'
import { PlannerFrame } from '@planner/shell/frame'
import { usePlannerKeys } from '@planner/shell/use-planner-keys'
import { DecisionBody } from '@planner/shell/lenses/decisions'
import { IdeaBody } from '@planner/shell/lenses/ideas'
import { InboxFocus } from '@planner/shell/lenses/inbox'
import { RiskBody } from '@planner/shell/lenses/risks'
import { RoadmapBody } from '@planner/shell/lenses/roadmap'
import { StreamBody } from '@planner/shell/lenses/streams'
import type { NeedYou, PlannerDb, PlannerItem } from '@planner/api/types'

const REFRESH_MS = 30_000

const SEVERITY_DOT: Record<string, DotState> = { critical: 'blocked', high: 'blocked', medium: 'review', low: 'todo' }
const DECIDED = new Set(['accepted', 'decided', 'superseded', 'reversed', 'rejected'])
const str = (v: unknown) => (typeof v === 'string' ? v : '')

/** The spine's groups and rows for one lens. Rows say what the golden's spine says for that lens. */
export function lensGroups(db: PlannerDb, items: readonly PlannerItem[], needs: (i: PlannerItem) => boolean, now: number): SpineGroup[] {
  const pick = (f: (i: PlannerItem) => boolean) => items.filter(f)
  const entry = (i: PlannerItem, extra: Partial<SpineGroup['items'][number]> = {}) => ({ id: i.id, title: i.title, ...extra })
  const chip = (t: string) => <Chip square>{t}</Chip>
  const quiet = (t: ReactNode) => <span className="text-label text-fg-muted">{t}</span>
  const drop = (gs: SpineGroup[]) => gs.filter((g) => g.items.length > 0)
  switch (db) {
    case 'workstreams': {
      const row = (i: PlannerItem) => entry(i, { flags: str(i.phase) ? chip(str(i.phase)) : undefined })
      return drop([
        { id: 'active', title: TEXT.groupActive, items: pick((i) => i.status === 'active').map(row) },
        { id: 'quiet', title: TEXT.groupPausedDraft, collapsed: true, items: pick((i) => i.status === 'paused' || i.status === 'draft').map(row) },
        { id: 'closed', title: TEXT.groupClosed, collapsed: true, items: pick((i) => i.status === 'closed').map(row) },
      ])
    }
    case 'roadmap': {
      const row = (i: PlannerItem) => entry(i, { flags: i.mvp === true ? chip(TEXT.mvp) : undefined, meta: str(i.area) ? <span className={CAPTION}>{str(i.area)}</span> : undefined })
      const live = (i: PlannerItem) => i.status !== 'completed' && i.status !== 'cancelled'
      return drop([
        { id: 'now', title: TEXT.horizonNow, items: pick((i) => live(i) && i.horizon === 'now').map(row) },
        { id: 'next', title: TEXT.horizonNext, items: pick((i) => live(i) && i.horizon === 'next').map(row) },
        { id: 'later', title: TEXT.horizonLater, collapsed: true, items: pick((i) => live(i) && i.horizon === 'later').map(row) },
        { id: 'done', title: TEXT.groupDelivered, collapsed: true, items: pick((i) => !live(i)).map(row) },
      ])
    }
    case 'ideas': {
      const row = (i: PlannerItem) => entry(i, { meta: <span className="flex items-center gap-2">{str(i.kind) && chip(str(i.kind))}{str(i.discuss_in) && quiet(str(i.discuss_in))}</span> })
      return drop([
        { id: 'discussing', title: TEXT.groupDiscussing, items: pick((i) => i.status === 'exploring' || i.status === 'discussing').map(row) },
        { id: 'new', title: TEXT.groupNew, items: pick((i) => i.status === 'draft').map(row) },
        { id: 'archived', title: TEXT.groupArchived, collapsed: true, items: pick((i) => i.status !== 'draft' && i.status !== 'exploring' && i.status !== 'discussing').map(row) },
      ])
    }
    case 'decisions': {
      const open = (i: PlannerItem) => !DECIDED.has(i.status ?? '')
      const row = (i: PlannerItem) => entry(i, {
        leading: <StateDot state={open(i) ? 'review' : 'done'} className="self-center" />,
        meta: quiet(open(i) ? [i.options?.length ? TEXT.optionCount(i.options.length) : '', boardAge(i.created ?? i.updated ?? '', now)].filter(Boolean).join(' · ') : str(i.date)),
      })
      return drop([
        { id: 'open', title: TEXT.groupNeedsDecision, items: pick(open).map(row) },
        { id: 'decided', title: TEXT.groupDecided, collapsed: true, items: pick((i) => !open(i)).map(row) },
      ])
    }
    case 'risks': {
      const row = (i: PlannerItem) => entry(i, {
        leading: <StateDot state={SEVERITY_DOT[str(i.severity)] ?? 'todo'} className="self-center" />,
        meta: <Pips tone="danger" level={['low', 'medium', 'high', 'critical'].indexOf(str(i.severity)) + 1} of={4} label={str(i.severity)} className="ml-5" />,
      })
      return drop([
        { id: 'open', title: TEXT.groupOpen, items: pick((i) => i.status === 'open').map(row) },
        { id: 'handled', title: TEXT.groupMitigatingResolved, collapsed: true, items: pick((i) => i.status !== 'open').map(row) },
      ])
    }
    case 'inbox':
      return drop([
        { id: 'new', title: TEXT.groupToTriage, items: pick((i) => i.status === 'new').map((i) => entry(i, { meta: str(i.kind) ? <span className="flex w-fit">{chip(str(i.kind))}</span> : undefined })) },
        { id: 'archived', title: TEXT.groupArchived, collapsed: true, items: pick((i) => i.status !== 'new').map((i) => entry(i, { meta: str(i.kind) ? <span className="flex w-fit">{chip(str(i.kind))}</span> : undefined })) },
      ])
    default:
      return []
  }
  void needs
}

const KIND: Record<string, string> = { workstreams: 'Workstream', roadmap: 'Roadmap', ideas: 'Idea', decisions: 'Decision', risks: 'Risk', inbox: 'Inbox' }

/** One of the non-Themes lenses on the shared Planner frame: a spine of the database's items and a focus pane shaped by the lens. */
export default function LensPage({ db }: { db: PlannerDb }) {
  const api = usePlannerApi()
  const navigate = useNavigate()
  const { id } = useParams<{ id?: string }>()
  const { lastEvent } = usePlannerEvents()
  const schema = usePlannerSchema(db)
  const [items, setItems] = useState<PlannerItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [goodAt, setGoodAt] = useState<number | null>(null)
  const [counts, setCounts] = useState<Partial<Record<PlannerDb, number>>>({})
  const [need, setNeed] = useState<NeedYou | null>(null)
  const [portfolio, setPortfolio] = useState<{ done: number; total: number } | undefined>()
  const [needOnly, setNeedOnly] = useState(false)
  const [ctxCollapsed, setCtxCollapsed] = useState(false)
  const [details, setDetails] = useState(false)
  const [palette, setPalette] = useState(false)
  const [creating, setCreating] = useState<'themes' | 'ideas' | null>(null)
  const [createTitle, setCreateTitle] = useState('')
  const [links, setLinks] = useState<ContextLink[]>([])
  const lens = LENSES.find((l) => l.db === db)!
  const openId = id ? decodeURIComponent(id) : undefined

  const load = useCallback(async () => {
    try {
      setItems(await api.plannerList(db))
      setGoodAt(Date.now())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : TEXT.loadFailed)
    }
  }, [api, db])
  useEffect(() => {
    setItems(null)
    void load()
    api.plannerDatabases().then((d) => setCounts(Object.fromEntries(d.map((x) => [x.name, x.count]))), () => undefined)
    api.plannerNeedYou().then(setNeed, () => setNeed(null))
    api.plannerThemeBoard().then((b) => setPortfolio(b.progress), () => undefined)
    const t = setInterval(() => void load(), REFRESH_MS)
    return () => clearInterval(t)
  }, [load, lastEvent, api])

  const needIds = useMemo(() => new Set((need?.items ?? []).filter((n) => n.db === db).map((n) => n.id)), [need, db])
  const item = items?.find((i) => i.id === openId) ?? (db === 'inbox' ? items?.find((i) => i.status === 'new') : undefined)
  useEffect(() => {
    if (!item) return setLinks([])
    let live = true
    api.plannerLinks(db, item.id).then((v) => live && setLinks([
      ...v.outbound.map((l) => ({ kind: l.kind, label: l.title, note: l.status })),
      ...v.inbound.map((l) => ({ kind: l.kind, label: l.title, note: TEXT.linkedFromNote })),
      ...v.external.map((l) => ({ kind: l.kind, label: l.label })),
    ]), () => live && setLinks([]))
    return () => { live = false }
  }, [api, db, item?.id, item?.rev])

  const shown = useMemo(() => (items ?? []).filter((i) => !needOnly || needIds.has(i.id)), [items, needOnly, needIds])
  const groups = useMemo(() => lensGroups(db, shown, (i) => needIds.has(i.id), Date.now()), [db, shown, needIds])
  const open = (next: string) => navigate(lensPath(db, next))

  // On a wide screen the focus pane is never left empty: with nothing open, open the first item of the spine.
  // (Narrow screens keep the list, since opening an item replaces it.)
  const first = groups.find((g) => !g.collapsed && g.items.length > 0)?.items[0] ?? groups.find((g) => g.items.length > 0)?.items[0]
  useEffect(() => {
    if (openId || !first || typeof window.matchMedia !== 'function' || !window.matchMedia('(min-width: 640px)').matches) return
    navigate(lensPath(db, first.id), { replace: true })
  }, [openId, first?.id, db])

  // A write that lost to a newer revision reloads and says so; anything else is reported as it came.
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false) // the ref closes the gap before the state renders
  const write = async (fn: () => Promise<unknown>) => {
    if (inFlight.current) return // a write is in flight: never submit twice
    inFlight.current = true
    setBusy(true)
    try {
      await fn()
    } catch (err) {
      notifyError(err, TEXT.writeFailed)
    } finally {
      await load()
      inFlight.current = false
      setBusy(false)
    }
  }

  usePlannerKeys({
    palette: () => setPalette(true),
    create: () => setCreating('themes'),
    link: () => undefined,
    lens: (k) => {
      if (k === 'n') return setNeedOnly(true)
      const l = LENSES.find((x) => x.key === k)
      if (l) navigate(lensPath(l.db))
    },
    view: () => undefined,
    pin: () => item && void togglePin(item),
    finish: () => undefined,
    step: (delta) => {
      const rows = [...document.querySelectorAll<HTMLElement>('[role="listbox"] [role="option"]')]
      const at = rows.findIndex((r) => r.getAttribute('aria-selected') === 'true')
      const next = rows[Math.max(0, Math.min(rows.length - 1, at + delta))]
      next?.click()
      next?.focus()
    },
    clearFilter: () => (needOnly ? (setNeedOnly(false), true) : false),
  }, true)

  const togglePin = (i: PlannerItem) => write(() => api.plannerPin(db, i.id, !i.pinned, i.rev))
  const commands = useCallback(
    (q: string): PaletteCommand[] => [
      { id: 'create:themes', mode: 'create', text: TEXT.createTheme, label: q ? TEXT.createThemeIn(q) : TEXT.createTheme, group: TEXT.paletteKindTheme, hint: '↵', always: true },
      { id: 'create:ideas', mode: 'create', text: TEXT.createIdea, label: q ? TEXT.createIdeaNamed(q) : TEXT.createIdea, group: TEXT.paletteKindIdea, hint: 'I', always: true },
      ...(items ?? []).map((r) => ({ id: `open:${r.id}`, mode: 'open', group: KIND[db], text: r.title, label: r.title })),
    ],
    [items, db],
  )

  const context = item && (
    <Context
      db={db}
      id={item.id}
      rev={item.rev}
      pinned={Boolean(item.pinned)}
      comments={item.comments ?? []}
      links={links}
      onCommented={() => void load()}
      mentionable={(items ?? []).map((i) => ({ id: i.id, title: i.title, ref: typeof i.legacy_id === 'string' ? i.legacy_id : undefined }))}
      statusOptions={(schema?.enums.status ?? []).map((s) => ({ value: s, label: sentenceCase(s) }))}
      onStatus={async (status) => { await api.plannerUpdate(db, item.id, { status }, item.rev + 1) }}
    />
  )
  const selected = Boolean(item) || db === 'inbox'
  const body = db === 'inbox'
    ? <InboxFocus items={(items ?? []).filter((i) => i.status === 'new')} selectedId={openId} loading={items === null} onChanged={load} />
    : item
      ? <LensFocus db={db} item={item} statuses={schema?.enums.status ?? []} severities={schema?.enums.severity ?? []} write={write} onPin={() => void togglePin(item)}>
          {db === 'workstreams' && <StreamBody item={item} />}
          {db === 'roadmap' && <RoadmapBody item={item} horizons={schema?.enums.horizon ?? []} siblings={items ?? []} write={write} />}
          {db === 'ideas' && <IdeaBody item={item} write={write} />}
          {db === 'decisions' && <DecisionBody item={item} write={write} busy={busy} />}
          {db === 'risks' && <RiskBody item={item} statuses={schema?.enums.status ?? []} write={write} />}
        </LensFocus>
      : <p className="m-auto text-xs text-fg-muted">{items === null ? '' : TEXT.selectItemFor(lens.label)}</p>

  return (
    <>
      <PlannerFrame
        active={db}
        onLens={(next) => navigate(lensPath(next))}
        counts={counts}
        portfolio={portfolio}
        needYou={{ count: need?.total ?? 0, active: needOnly, onToggle: () => setNeedOnly((v) => !v) }}
        freshAt={goodAt}
        stale={Boolean(error)}
        onPalette={() => setPalette(true)}
        onNew={() => setCreating('themes')}
        onDetails={() => setDetails(true)}
        contextCollapsed={ctxCollapsed}
        onToggleContext={() => setCtxCollapsed((v) => !v)}
        focused={selected && (Boolean(item) || db === 'inbox')}
        onBack={() => navigate(lensPath(db))}
        spine={items ? <SpineList label={TEXT.spineLabel} groups={groups} selectedId={openId} onSelect={open} pendingLabel={TEXT.saving} pinnedLabel={TEXT.pinned} emptyLabel={needOnly ? TEXT.needYouEmpty : TEXT.lensEmpty(lens.label)} /> : error ? null : <div className="flex flex-col gap-2 p-1" aria-busy>{[0, 1, 2].map((i) => <Skeleton key={i} className="h-10 w-full" />)}</div>}
        spineNotice={error ? (
          <p role="alert" className="border-t border-border-subtle px-3 py-2 text-caption text-danger">
            {error} <button type="button" className="underline" onClick={() => void load()}>{TEXT.retry}</button>
          </p>
        ) : null}
        context={context}
      >
        {body}
      </PlannerFrame>
      <Sheet open={details && Boolean(item)} onOpenChange={setDetails}>
        <SheetContent className="lg:hidden">
          <SheetTitle className="sr-only">{TEXT.detailsLabel}</SheetTitle>
          {context}
        </SheetContent>
      </Sheet>
      <CommandPalette
        open={palette}
        onOpenChange={setPalette}
        title={TEXT.paletteTitle}
        modes={[{ id: 'create', label: TEXT.modeCreate }, { id: 'open', label: TEXT.modeOpen }]}
        commands={commands}
        emptyLabel={TEXT.paletteEmpty}
        hints={<><PaletteHint k="↵">{TEXT.hintEnter}</PaletteHint><PaletteHint k="@">{TEXT.hintAt}</PaletteHint><PaletteHint k="esc">{TEXT.hintEsc}</PaletteHint></>}
        onRun={(cmd, q) => {
          if (cmd.startsWith('open:')) open(cmd.slice(5))
          else if (cmd === 'create:themes' || cmd === 'create:ideas') (setCreateTitle(q), setCreating(cmd === 'create:ideas' ? 'ideas' : 'themes'))
        }}
      />
      <PlannerCreateDialog db={creating ?? 'themes'} initialTitle={createTitle} open={creating !== null} onOpenChange={(o) => !o && setCreating(null)} nextRank={1} onCreated={(it) => { void load(); navigate(lensPath(creating ?? 'themes', it.id)) }} />
    </>
  )
}

/** The common head of a lens focus pane: id chip, kind, pin, title, status, description, then the lens body. */
function LensFocus({ db, item, statuses, severities, write, onPin, children }: { db: PlannerDb; item: PlannerItem; statuses: string[]; severities: string[]; write: (fn: () => Promise<unknown>) => Promise<void>; onPin: () => void; children: ReactNode }) {
  const api = usePlannerApi()
  const short = item.legacy_id && typeof item.legacy_id === 'string' ? item.legacy_id : item.id.slice(0, 8)
  const edit = (patch: Record<string, unknown>) => void write(() => api.plannerUpdate(db, item.id, patch, item.rev))
  return (
    <section aria-labelledby="lens-title" className="flex min-h-0 min-w-0 flex-1 flex-col gap-5 overflow-y-auto px-6 py-5 max-sm:px-4 max-sm:py-4">
      <header className="flex flex-col gap-2.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <Chip square title={item.id}>{short}</Chip>
          <span className={CAPTION}>{KIND[db]}{typeof item.kind === 'string' ? ` · ${item.kind}` : typeof item.area === 'string' ? ` · ${item.area}` : ''}</span>
          <span className="flex-1" />
          <button type="button" aria-pressed={Boolean(item.pinned)} onClick={onPin} className="rounded-full">
            <Chip selected={Boolean(item.pinned)} className="h-6 cursor-pointer hover:bg-selection"><Pin className="size-3" />{TEXT.pin}</Chip>
          </button>
          <OverflowMenu ariaLabel={TEXT.moreActions} actions={[{ label: TEXT.copyLink, onSelect: () => void navigator.clipboard?.writeText(`${location.origin}${lensPath(db, item.id)}`) }]} />
        </div>
        <div className="flex flex-wrap items-center gap-2.5">
          <InlineEdit as="h1" id="lens-title" label={TEXT.titleLabel} value={item.title} onCommit={(title) => edit({ title })} className="min-w-0 font-sans text-xl font-semibold normal-case leading-tight tracking-normal text-fg" />
          {db === 'risks' ? (
            typeof item.severity === 'string' && <span className="flex items-center gap-2 text-control text-fg-muted">{TEXT.severity}<Pips tone="danger" level={Math.max(0, (severities.indexOf(item.severity)) + 1)} of={Math.max(severities.length, 1)} label={item.severity} /></span>
          ) : (
            <StatusPick status={item.status ?? ''} statuses={statuses} onPick={(status) => edit({ status })} />
          )}
          {item.mvp === true && <Chip square>{TEXT.mvp}</Chip>}
          {typeof item.discuss_in === 'string' && item.discuss_in && <span className="flex items-center gap-1.5 text-control text-fg-muted">{TEXT.discussIn}<Chip square>{item.discuss_in}</Chip></span>}
          {db === 'decisions' && !DECIDED.has(item.status ?? '') && item.created && <span className="text-control text-fg-muted">{TEXT.waitingDays(Math.max(0, Math.floor((Date.now() - Date.parse(String(item.created))) / 86_400_000)))}</span>}
          {db === 'decisions' && typeof item.owner === 'string' && item.owner && <span className="flex items-center gap-1.5 text-control text-fg-muted">{TEXT.owner}<Chip square>@{item.owner}</Chip></span>}
        </div>
        {item.tldr && <InlineEdit as="p" multiline label={TEXT.descriptionLabel} value={item.tldr} onCommit={(tldr) => edit({ tldr })} className="max-w-prose text-control text-fg-muted" />}
      </header>
      {children}
    </section>
  )
}

function StatusPick({ status, statuses, onPick }: { status: string; statuses: string[]; onPick: (s: string) => void }) {
  const chip = (
    <Chip tone="strong" className="h-6 cursor-pointer">
      <StateDot state="doing" />
      {status ? sentenceCase(status) : TEXT.noStatus}
      {statuses.length > 0 && <ChevronDown className="size-3" />}
    </Chip>
  )
  if (statuses.length === 0) return chip
  return <StatusMenu trigger={chip} triggerLabel={`${status}. ${TEXT.moveTo}`} heading={TEXT.moveTo} current={status} options={statuses.map((s) => ({ value: s, label: sentenceCase(s) }))} onSelect={onPick} />
}

export { createPlannerId }
