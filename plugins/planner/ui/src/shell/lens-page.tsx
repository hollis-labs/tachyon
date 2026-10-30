import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Button, Skeleton, Textarea } from '@hollis-labs/design-components'
import { StatusBadge } from '@hollis-labs/kit-dashboard'
import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { createPlannerId } from '@planner/api/identity'
import { usePlannerApi, usePlannerEvents } from '@planner/api/context'
import { notifyError, notifySuccess } from '@planner/api/notifications'
import { usePlannerSchema } from '@planner/hooks/use-planner-schema'
import { statusToneKey } from '@planner/lib/planner'
import { LENSES, TEXT, TRIAGE_ROUTES } from '@planner/lib/vocabulary'
import { OptionCompare, SegmentedProgress, SpineList, StatusMenu, TriageStack, type SpineGroup } from '@planner/shared'
import type { PlannerDb, PlannerItem, PlannerWorkstreamTasks } from '@planner/api/types'

const REFRESH_MS = 30_000

/** One of the non-Themes lenses: a spine of the database's items and a focus pane shaped by the lens. */
export default function LensPage({ db }: { db: PlannerDb }) {
  const api = usePlannerApi()
  const navigate = useNavigate()
  const { id } = useParams<{ id?: string }>()
  const { lastEvent } = usePlannerEvents()
  const schema = usePlannerSchema(db)
  const [items, setItems] = useState<PlannerItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const lens = LENSES.find((l) => l.db === db)!
  const openId = id ? decodeURIComponent(id) : undefined

  const load = useCallback(async () => {
    try {
      setItems(await api.plannerList(db))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : TEXT.loadFailed)
    }
  }, [api, db])
  useEffect(() => {
    setItems(null)
    void load()
    const t = setInterval(() => void load(), REFRESH_MS)
    return () => clearInterval(t)
  }, [load, lastEvent])

  const item = items?.find((i) => i.id === openId)
  const groups = useMemo<SpineGroup[]>(() => {
    const by = new Map<string, PlannerItem[]>()
    for (const it of items ?? []) by.set(it.status ?? '', [...(by.get(it.status ?? '') ?? []), it])
    return [...by].map(([status, list]) => ({ id: status || 'none', title: status || '—', items: list.map((i) => ({ id: i.id, title: i.title })) }))
  }, [items])
  const open = (next: string) => navigate(`/planner/${db}/${encodeURIComponent(next)}`)
  const selected = Boolean(item)

  // A write that lost to a newer revision reloads and says so; anything else is reported as it came.
  const write = async (fn: () => Promise<unknown>) => {
    try {
      await fn()
    } catch (err) {
      notifyError(err, TEXT.writeFailed)
    } finally {
      await load()
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full bg-bg">
      <nav aria-label={TEXT.appName} className={`${selected ? 'hidden lg:flex' : 'flex'} min-h-0 w-full shrink-0 flex-col border-r border-border lg:w-72`}>
        <div className="overflow-x-auto border-b border-border px-3 py-2">
          <DashboardViewTabs label={TEXT.lensesLabel} value={db} items={LENSES.map((l) => ({ value: l.db, label: l.label }))} onChange={(v) => navigate(`/planner/${v === 'themes' ? 'themes' : v}`)} />
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {items ? <SpineList label={TEXT.spineLabel} groups={groups} selectedId={openId} onSelect={open} emptyLabel={TEXT.lensEmpty(lens.label)} /> : <div className="flex flex-col gap-2 p-3" aria-busy>{[0, 1, 2].map((i) => <Skeleton key={i} className="h-10 w-full" />)}</div>}
        </div>
        {error && (
          <p role="alert" className="border-t border-border px-3 py-2 dash-caption text-danger">
            {error} <button type="button" className="underline" onClick={() => void load()}>{TEXT.retry}</button>
          </p>
        )}
      </nav>
      <main className={`${selected || db === 'inbox' ? 'flex' : 'hidden lg:flex'} min-h-0 min-w-0 flex-1 flex-col overflow-y-auto`}>
        {db === 'inbox' ? (
          <InboxFocus items={(items ?? []).filter((i) => i.status === 'new')} loading={items === null} onChanged={load} />
        ) : item ? (
          <section aria-labelledby="lens-title" className="flex flex-col gap-4 p-4">
            <header className="flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <button type="button" onClick={() => navigate(`/planner/${db}`)} className="dash-caption text-fg-muted hover:text-fg lg:hidden">← {TEXT.back}</button>
                <span className="rounded-md border border-border px-1.5 font-mono dash-caption text-fg-muted">{item.id}</span>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <h1 id="lens-title" className="min-w-0 flex-1 text-lg font-medium text-fg">{item.title}</h1>
                <StatusPick db={db} item={item} statuses={schema?.enums.status ?? []} write={write} />
              </div>
              {item.tldr && <p className="dash-support text-fg">{item.tldr}</p>}
            </header>
            {db === 'workstreams' && <StreamBody item={item} />}
            {db === 'roadmap' && <RoadmapBody item={item} horizons={schema?.enums.horizon ?? []} write={write} />}
            {db === 'ideas' && <IdeaBody item={item} write={write} />}
            {db === 'decisions' && <DecisionBody item={item} write={write} />}
            {db === 'risks' && <RiskBody item={item} levels={schema?.enums.severity ?? []} />}
          </section>
        ) : (
          <p className="m-auto dash-support text-fg-muted">{TEXT.selectItem}</p>
        )}
      </main>
    </div>
  )
}

function StatusPick({ db, item, statuses, write }: { db: PlannerDb; item: PlannerItem; statuses: string[]; write: (fn: () => Promise<unknown>) => Promise<void> }) {
  const api = usePlannerApi()
  if (statuses.length === 0) return <StatusBadge status={item.status ?? ''} tone={statusToneKey(item.status)} />
  return (
    <StatusMenu
      trigger={<StatusBadge status={item.status ?? ''} tone={statusToneKey(item.status)} />}
      triggerLabel={`${item.status ?? ''}. ${TEXT.moveTo}`}
      heading={TEXT.moveTo}
      current={item.status ?? ''}
      options={statuses.map((s) => ({ value: s, label: s }))}
      onSelect={(status) => void write(() => api.plannerUpdate(db, item.id, { status }, item.rev))}
    />
  )
}

function StreamBody({ item }: { item: PlannerItem }) {
  const api = usePlannerApi()
  const [t, setT] = useState<PlannerWorkstreamTasks | null>(null)
  useEffect(() => {
    let live = true
    setT(null)
    api.plannerWorkstreamTasks(item.id).then((r) => live && setT(r), () => live && setT(null))
    return () => { live = false }
  }, [api, item.id])
  if (!t) return <Skeleton className="h-16 w-full" />
  const c = t.counts
  const done = c.done ?? 0
  const blocked = c.blocked ?? 0
  const doing = c.doing ?? 0
  const review = c.review ?? 0
  const label = TEXT.progressText(done, t.total, blocked)
  return (
    <div className="flex flex-col gap-3">
      <SegmentedProgress label={label} items={[{ state: 'done', count: done }, { state: 'doing', count: doing }, { state: 'review', count: review }, { state: 'blocked', count: blocked }, { state: 'todo', count: Math.max(0, t.total - done - doing - review - blocked) }]} />
      <p className="dash-caption text-fg-muted">{label} · {TEXT.streamNote}</p>
      <h2 className="font-mono dash-caption uppercase tracking-label text-fg-faint">{TEXT.streamTasks}</h2>
      {t.open.length === 0 ? <p className="dash-support text-fg-muted">{TEXT.streamNoTasks}</p> : (
        <ul className="flex flex-col gap-1">{t.open.map((o) => <li key={o.id} className="dash-support text-fg"><span className="mr-2 font-mono dash-caption text-fg-faint">{o.id}</span>{o.title}<span className="ml-2 dash-caption text-fg-muted">{o.status}</span></li>)}</ul>
      )}
    </div>
  )
}

function RoadmapBody({ item, horizons, write }: { item: PlannerItem; horizons: string[]; write: (fn: () => Promise<unknown>) => Promise<void> }) {
  const api = usePlannerApi()
  const current = typeof item.horizon === 'string' ? item.horizon : ''
  if (horizons.length === 0) return null
  return (
    <div className="flex flex-col gap-2">
      <h2 className="font-mono dash-caption uppercase tracking-label text-fg-faint">{TEXT.horizon}</h2>
      <DashboardViewTabs variant="segments" label={TEXT.horizonLabel} value={current} items={horizons.map((h) => ({ value: h, label: h }))} onChange={(horizon) => void write(() => api.plannerUpdate('roadmap', item.id, { horizon }, item.rev))} />
    </div>
  )
}

function IdeaBody({ item, write }: { item: PlannerItem; write: (fn: () => Promise<unknown>) => Promise<void> }) {
  const api = usePlannerApi()
  const [notes, setNotes] = useState(item.notes ?? '')
  useEffect(() => setNotes(item.notes ?? ''), [item.id, item.rev]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = () => notes !== (item.notes ?? '') && void write(() => api.plannerUpdate('ideas', item.id, { notes }, item.rev))
  return (
    <div className="flex flex-col gap-3">
      <label className="font-mono dash-caption uppercase tracking-label text-fg-faint" htmlFor="idea-notes">{TEXT.notes}</label>
      <Textarea id="idea-notes" rows={10} value={notes} onChange={(e) => setNotes(e.target.value)} onBlur={save} onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); save() } }} />
      <p className="flex items-center gap-2 dash-caption text-fg-faint" title={TEXT.promoteSoon}>
        {TEXT.promoteTo}:
        {['Theme', 'Roadmap item', 'Decision'].map((l) => <Button key={l} size="sm" variant="outline" disabled>{l}</Button>)}
      </p>
    </div>
  )
}

function DecisionBody({ item, write }: { item: PlannerItem; write: (fn: () => Promise<unknown>) => Promise<void> }) {
  const api = usePlannerApi()
  const options = item.options ?? []
  const chosen = typeof item.chosen === 'string' ? item.chosen : typeof item.decision === 'string' ? item.decision : item.status === 'decided' ? '' : undefined
  if (options.length === 0) return <p className="dash-support text-fg-muted">{TEXT.noOptions}</p>
  return <OptionCompare options={options} chosenId={chosen || undefined} onChoose={(opt) => void write(() => api.plannerDecide(item.id, opt, item.rev))} labels={{ choose: TEXT.chooseOption, chooseButton: TEXT.chooseButton, recommended: TEXT.recommended, chosen: TEXT.chosen, pros: TEXT.pros, cons: TEXT.cons, groupLabel: TEXT.optionsGroup }} />
}

function RiskBody({ item, levels }: { item: PlannerItem; levels: string[] }) {
  const sev = typeof item.severity === 'string' ? item.severity : ''
  const at = Math.max(0, levels.indexOf(sev))
  return (
    <div className="flex flex-col gap-2">
      <p className="flex items-center gap-2 dash-support text-fg">
        <span className="font-mono dash-caption uppercase tracking-label text-fg-faint">{TEXT.severity}</span>
        <span aria-hidden className="flex gap-1">{levels.map((l, i) => <span key={l} className={`size-2 rounded-full ${i <= at && sev ? 'bg-fg' : 'bg-surface-hover'}`} />)}</span>
        {sev}
      </p>
      {(item.related_ids ?? []).length > 0 && <p className="dash-caption text-fg-muted">{TEXT.mitigatedBy}: {item.related_ids!.join(', ')}</p>}
    </div>
  )
}

function InboxFocus({ items, loading, onChanged }: { items: PlannerItem[]; loading: boolean; onChanged: () => Promise<void> }) {
  const api = usePlannerApi()
  const [busy, setBusy] = useState(false)
  const [targets] = useState(() => new Map<string, string>()) // one target id per (card, route), so a retry never creates twice
  const card = items[0]
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
    } catch (err) {
      notifyError(err, TEXT.writeFailed)
    } finally {
      setBusy(false)
      await onChanged()
    }
  }
  if (loading) return <Skeleton className="m-4 h-32" />
  return (
    <div className="p-4">
      <TriageStack
        card={card ? <div className="flex flex-col gap-1"><h1 className="dash-support font-medium text-fg">{card.title}</h1>{card.tldr && <p className="dash-support text-fg-muted">{card.tldr}</p>}</div> : null}
        remaining={items.length}
        nextTitle={items[1]?.title}
        routes={TRIAGE_ROUTES}
        busy={busy}
        onRoute={(to) => card && void run(async () => {
          const key = `${card.id}:${to}`
          const targetId = targets.get(key) ?? createPlannerId()
          targets.set(key, targetId)
          const res = await api.plannerInboxPromote(card.id, to as PlannerDb, card.rev, targetId)
          notifySuccess(TEXT.promoted(res.item.id))
        })}
        onDismiss={() => card && void run(() => api.plannerInboxDismiss(card.id, card.rev))}
        labels={{ count: TEXT.triageCount, allTriaged: TEXT.allTriaged, routeHeading: TEXT.triageInto, dismiss: TEXT.dismiss, next: TEXT.nextUp }}
      />
    </div>
  )
}
