import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Maximize2, Minimize2, X } from 'lucide-react'
import { CAPTION } from '@planner/shared'
import { Act } from '../cards/Block'
import { cn } from '@hollis-labs/design-components'
import { DEMO } from '../cards/useBlocks'
import { chatFetch, CHAT_BASE, chatJSON } from '../roster'
import { ScratchPane } from '../drawer/ScratchPane'
import type { ChatThread } from '../types'
import { useNarrow } from '../view/useNarrow'
import { useAutolink } from '../../autolink/context'
import { pathRefOf } from '../drawer/model'
import { useAside, type PanelId } from './context'
import { scopeKey } from './grants'
import { panelsFor, panelsVersion, resolvePanel, storePanel, storedPanel, subscribePanels } from './registry'
import { TabStrip } from './TabStrip'
import './PlannerPanel'
import { DEMO_DOC } from './fixtures'
import { useActivity, useBookmarks, usePlans, useRules, useThreadEvents, useTopics, useWork } from './data'
import { ArtifactsPanel } from './ArtifactsPanel'
import { DEMO_WORKSPACE } from '../drawer/model'
import { ActivityPanel, ApprovalsPanel, BookmarksPanel, DocPanel, FilesPanel, PlanPanel, TopicsPanel, WorkPanel } from './Panels'

const TEXTY = /^(text\/|application\/(json|xml|x-yaml|yaml))|\.(md|txt|json|ya?ml|ts|tsx|go|css|html)$/i

/**
 * chat.aside: the auto aside. A panel strip over the open panel. The agent opens panels (plan written -> Plan, approval
 * pending -> Approvals, document shared -> Doc); a manual pick locks until the user returns to auto. It never takes
 * focus and never switches while the user types in it. Above 1024 it is a column; below, an overlay; at 390 a bottom sheet.
 */
export function Aside({ thread, scratchInAside, sessionRef }: { thread: ChatThread; scratchInAside: boolean; sessionRef?: string }) {
  const narrow = useNarrow()
  const a = useAside()
  const ref = sessionRef
  const conv = a.conv
  const root = useRef<HTMLElement | null>(null)
  a.rootRef.current = root.current
  a.setScope(scopeKey(thread.agent.agentId, ref))

  const plans = usePlans(ref, a.tick)
  const work = useWork(ref, thread.agent.agentId, a.tick)
  const activity = useActivity(ref, a.tick)
  const rules = useRules(ref, a.tick)
  const blocks = conv?.blocks ?? []
  const waiting = blocks.filter((b) => b.kind === 'approval' && !b.answered).length

  // Server frames: `panel` asks for a panel; everything else is a prompt to refetch.
  useThreadEvents(ref, (f) => {
    if (f.type === 'panel' && typeof f.panel === 'string') a.auto(f.panel as PanelId, f.source === 'agent' ? 'agent' : 'system')
    if (['plan', 'activity', 'topic', 'bookmark', 'workspace', 'card', 'reconnected'].includes(f.type)) a.bump()
  })
  // Agent wrote a plan -> Plan. Approval pending -> Approvals. (Frames may not arrive on every host; the read model is the fallback.)
  const hadPlan = useRef(false)
  useEffect(() => {
    const has = plans.plans.some((p) => p.steps.length > 0)
    if (has && !hadPlan.current && !plans.loading) a.auto('plan', 'opened by agent')
    hadPlan.current = has
    // biome-ignore lint/correctness/useExhaustiveDependencies: a.auto is stable enough; only the plan transition matters
  }, [plans.plans, plans.loading])
  // Approvals already waiting when the thread opens do not move the aside (the golden rests on Plan); a new one does.
  const lastWaiting = useRef<number | null>(null)
  const loaded = !!conv?.blocksLoaded
  useEffect(() => {
    if (!loaded) { lastWaiting.current = null; return }
    if (lastWaiting.current !== null && waiting > lastWaiting.current) a.auto('approvals', 'opened by agent')
    lastWaiting.current = waiting
    // biome-ignore lint/correctness/useExhaustiveDependencies: only the count matters
  }, [waiting, loaded])

  const autolink = useAutolink()
  const openDocFor = async (i: { id: string; name: string; mime?: string | null; shared: boolean; type?: string; ref?: string | null }) => {
    // A host path is the File viewer's: open its ref drawer, ship no second viewer.
    const path = pathRefOf(i)
    if (path) { autolink.openDrawer({ kind: 'path', canonical: path }); return }
    if (!ref) return
    if (!TEXTY.test(i.mime ?? i.name)) { a.openDoc({ name: i.name, text: `${i.name} can't be previewed here. Use Download from the Workspace.`, itemId: i.id, shared: i.shared }); return }
    try {
      const res = await chatFetch(`${CHAT_BASE}/threads/${encodeURIComponent(ref)}/workspace/${encodeURIComponent(i.id)}/content`)
      a.openDoc({ name: i.name, text: res.ok ? await res.text() : `Could not open ${i.name} (${res.status}).`, itemId: i.id, shared: i.shared })
    } catch { a.openDoc({ name: i.name, text: `Could not open ${i.name}.`, itemId: i.id, shared: i.shared }) }
  }
  const shareDoc = async () => {
    const d = a.doc
    if (!d?.itemId || !ref) return
    try { await chatJSON(`/threads/${encodeURIComponent(ref)}/workspace/${encodeURIComponent(d.itemId)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ shared: !d.shared }) }); a.openDoc({ ...d, shared: !d.shared }) } catch { /* the doc stays as it was */ }
  }

  const marks = conv?.marks ?? { bookmarks: [], toggleBookmark: () => undefined, topics: [], addTopic: () => undefined }
  const planSteps = plans.plans.flatMap((p) => p.steps)
  const counts: Partial<Record<PanelId, string>> = {
    plan: planSteps.length ? `${planSteps.filter((s) => s.status === 'done').length}/${planSteps.filter((s) => s.status !== 'cancelled').length}` : undefined,
    topics: marks.topics.length ? String(marks.topics.length) : undefined,
    bookmarks: marks.bookmarks.length ? String(marks.bookmarks.length) : undefined,
    work: work.items.length ? String(work.items.length) : undefined,
    // The demo queue has one more fixture item than the stream (the golden's fs_write).
    approvals: waiting + (DEMO ? 1 : 0) ? String(waiting + (DEMO ? 1 : 0)) : undefined,
  }
  // The Agent Panel host: the panel set this thread shows (stored pick, else the agent's own, else the default).
  useSyncExternalStore(subscribePanels, panelsVersion)
  const scope = { agentId: thread.agent.agentId }
  const [pickedSet, setPickedSet] = useState<string | null>(() => storedPanel(ref))
  const panelSet = resolvePanel(scope, pickedSet)
  const sets = panelsFor(scope)
  const choosePanelSet = (id: string) => { setPickedSet(id); storePanel(ref, id); const first = sets.find((s) => s.id === id)?.tabs[0]; if (first) a.pick(first.id) }
  const builtin = panelSet.id === 'agent'
  const tabs = builtin && scratchInAside ? [...panelSet.tabs, { id: 'scratch', label: 'Scratch' }] : panelSet.tabs
  // Esc closes the full-window doc first.
  useEffect(() => {
    if (!a.full) return
    const on = (e: KeyboardEvent) => { if (e.key === 'Escape' && !e.defaultPrevented) { a.setFull(false) } }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [a.full, a.setFull])

  if (!a.open && !a.full) return null
  const body = (() => {
    switch (a.panel) {
      case 'plan': return <PlanPanel plans={plans.plans} loading={plans.loading} error={plans.error} threadRef={ref} setStatus={plans.setStatus} addStep={plans.addStep} createPlan={plans.createPlan} deletePlan={plans.deletePlan} reorderPlans={plans.reorderPlans} moveStep={plans.moveStep} onAsk={() => conv?.onInsert('Draft a plan for this chat.')} />
      case 'topics': return <TopicsPanel marks={marks} />
      case 'bookmarks': return <BookmarksPanel marks={marks} messages={conv?.messages ?? []} agent={thread.title} />
      case 'work': return <WorkPanel items={work.items} source={work.source} loading={work.loading} error={work.error} />
      case 'activity': return <ActivityPanel events={activity.events} hasMore={activity.hasMore} loading={activity.loading} error={activity.error} comment={activity.comment} more={activity.more} />
      case 'approvals': return <ApprovalsPanel blocks={blocks} commit={conv?.commit} agent={thread.title} rules={rules.rules} revoke={rules.revoke} />
      case 'files': return <FilesPanel threadRef={ref} demo={DEMO} onOpenDoc={(i) => void openDocFor(i)} />
      case 'doc': {
        const doc = a.doc ?? (DEMO ? DEMO_DOC : null)
        return <ArtifactsPanel threadRef={ref} demo={DEMO ? DEMO_WORKSPACE : undefined} doc={doc} docPanel={<DocPanel doc={doc} onShare={() => void shareDoc()} onClose={() => { a.setFull(false); a.openDoc(null); a.pick('doc'); a.setFull(false) }} />} onOpenRef={(i) => void openDocFor(i)} />
      }
      case 'scratch': return <ScratchPane threadRef={ref} onInsert={(t) => conv?.onInsert(t)} />
      default: return panelSet.tabs.find((t) => t.id === a.panel)?.render?.() ?? null
    }
  })()
  return (
    <aside
      ref={(el) => { root.current = el; a.rootRef.current = el }}
      aria-label="Agent Panel"
      className={cn(
        'flex min-h-0 w-[360px] shrink-0 flex-col border-l border-border-subtle bg-bg-elevated max-[1100px]:w-[300px]',
        narrow && 'absolute bottom-0 right-0 top-12 z-40 shadow-[-16px_0_40px_rgb(0_0_0/calc(.5*var(--sh)))]',
        'max-sm:fixed max-sm:inset-x-0 max-sm:bottom-0 max-sm:top-auto max-sm:z-50 max-sm:h-[72%] max-sm:w-full max-sm:rounded-t-panel max-sm:border-l-0 max-sm:border-t max-sm:border-t-surface-hover max-sm:shadow-[0_-16px_40px_rgb(0_0_0/calc(.5*var(--sh)))]',
        a.full && 'fixed! inset-0! top-0! z-50! h-auto! w-full! max-w-none rounded-none! border-0!',
      )}
    >
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border-subtle px-3">
        <span className={CAPTION} id="aside-title">Agent Panel</span>
        {sets.length > 1 && (
          <select aria-label="Panel" value={panelSet.id} onChange={(e) => choosePanelSet(e.target.value)} className="h-[22px] rounded-control border border-border bg-transparent px-1 text-label text-fg-secondary focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
            {sets.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}
          </select>
        )}
        <span className="flex-1" />
        <button type="button" aria-label={a.full ? 'Exit full window' : 'Full window'} aria-pressed={a.full} title="Full window" onClick={() => { a.setFull(!a.full) }} className="grid size-[22px] place-items-center rounded-control border border-border text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">{a.full ? <Minimize2 aria-hidden className="size-3" /> : <Maximize2 aria-hidden className="size-3" />}</button>
        <button type="button" aria-label="Close Agent Panel" onClick={() => { a.setFull(false); a.setOpen(false) }} className="grid size-[22px] place-items-center text-fg-muted hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring lg:hidden"><X aria-hidden className="size-3.5" /></button>
      </div>
      {a.pending && (
        <div role="group" aria-label="Agent request" className="flex flex-col gap-2 border-b border-border-subtle bg-selection px-3 py-2.5 text-xs text-fg-secondary">
          <p role="status">{thread.title} wants to open {(panelSet.tabs.find((t) => t.id === a.pending?.panel)?.label ?? a.pending.panel)}. You changed this view, so it is waiting for you.</p>
          <div className="flex flex-wrap gap-2"><Act primary onClick={() => a.answer('once')}>Once</Act><Act onClick={() => a.answer('session')}>This session</Act><Act onClick={() => a.answer('always')}>Always</Act><Act onClick={() => a.answer('deny')}>Not now</Act></div>
        </div>
      )}
      <TabStrip label="Panels" activeKey={a.panel}>
        {tabs.map((t) => (
          <button key={t.id} type="button" role="tab" id={`aside-tab-${t.id}`} aria-selected={a.panel === t.id} aria-controls="aside-panel" onClick={() => a.pick(t.id)} className={cn('inline-flex h-6 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 text-label text-fg-muted hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', a.panel === t.id && 'border border-border bg-selection text-fg')}>
            {t.label}{counts[t.id] && <small className="font-mono text-caption text-fg-faint">{counts[t.id]}</small>}
          </button>
        ))}
      </TabStrip>
      <div id="aside-panel" role="tabpanel" aria-labelledby={`aside-tab-${a.panel}`} className={cn('flex min-h-0 flex-1 flex-col gap-2.5 overflow-auto p-3', a.panel === 'doc' && 'items-stretch')}>{body}</div>
    </aside>
  )
}
