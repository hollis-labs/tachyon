import { useMemo, useRef, useState } from 'react'
import { Bookmark as BookmarkIcon, ChevronDown, ChevronRight, ClipboardCheck, FileText, ListFilter, MessageSquare, Plus, Search, ShieldCheck, SquareTerminal, Users, X, Zap, ArrowUpDown, ArrowUpRight, Check, Lock } from 'lucide-react'
import { CAPTION, Chip, Kbd, SegmentedProgress, StateDot } from '@planner/shared'
import { cn } from '@hollis-labs/design-components'
import { Act } from '../cards/Block'
import { useAnswer } from '../cards/useAnswer'
import type { Commit } from '../cards/Cards'
import type { StreamBlock } from '../cards/types'
import { MessageBody } from '../MessageBody'
import type { ChatMessage } from '../types'
import { chatJSON } from '../roster'
import { useWorkspace, type WorkspaceItem } from '../drawer/model'
import type { Activity, Plan, PlanStep, Rule, WorkItem } from './data'
import type { Marks } from './context'

const time = (at?: number) => (at ? new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : '')
const jump = (ref: string) => { const el = document.getElementById(ref); el?.scrollIntoView?.({ block: 'center' }); el?.focus?.() }
const Empty = ({ children, action }: { children: string; action?: React.ReactNode }) => (
  <div className="flex flex-col items-start gap-2 text-xs text-fg-muted"><p>{children}</p>{action}</div>
)
const Err = ({ children }: { children: string }) => (children ? <p role="alert" className="text-xs text-danger">{children}</p> : null)
const ROW = 'flex items-center gap-2 rounded-control px-1.5 py-[5px] text-xs text-fg-secondary hover:bg-selection'

// ---------------------------------------------------------------- Plan
export function PlanPanel({ plan, loading, error, setStatus, addStep, onAsk }: { plan: Plan | null; loading: boolean; error: string; setStatus: (s: PlanStep, st: PlanStep['status']) => void; addStep: (t: string) => Promise<void>; onAsk: () => void }) {
  const [adding, setAdding] = useState(false)
  const [title, setTitle] = useState('')
  const submit = async () => { const t = title.trim(); if (!t) return; setTitle(''); await addStep(t) }
  if (loading) return <p role="status" className="text-xs text-fg-muted">Loading the plan…</p>
  const steps = plan?.steps.filter((s) => s.status !== 'cancelled') ?? []
  const count = (st: PlanStep['status']) => steps.filter((s) => s.status === st).length
  const doing = steps.find((s) => s.status === 'doing')
  return (
    <>
      {steps.length === 0 && !adding ? (
        <Empty action={<Act onClick={onAsk}>Ask the agent to draft one</Act>}>No plan yet. Ask the agent to draft one.</Empty>
      ) : (
        <>
          <section className="flex flex-col gap-1.5">
            <span className={CAPTION}>Plan · this session</span>
            <SegmentedProgress label={`${count('done')} of ${steps.length} done`} items={[{ state: 'done', count: count('done') }, { state: 'doing', count: count('doing') }, { state: 'blocked', count: count('blocked') }, { state: 'todo', count: count('todo') }]} />
          </section>
          <ul className="flex flex-col gap-0.5">
            {steps.map((s) => {
              const done = s.status === 'done'
              return (
                <li key={s.id} className={cn('flex items-center gap-2 py-[3px] text-xs', done ? 'text-fg-muted line-through' : 'text-fg-secondary')}>
                  <button type="button" role="checkbox" aria-checked={done} aria-label={s.title} onClick={() => setStatus(s, done ? 'todo' : 'done')} className={cn('grid size-3 shrink-0 place-items-center rounded-sm border text-[9px] leading-none focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', done ? 'border-fg/60 bg-fg/60 text-bg' : s.status === 'doing' ? 'border-fg bg-fg/20' : 'border-fg-muted')}>{done ? '✓' : ''}</button>
                  <span className="min-w-0 flex-1 truncate">{s.title}</span>
                  {s.status === 'doing' && <Chip>doing</Chip>}
                  {s.status === 'blocked' && <Chip tone="danger">blocked</Chip>}
                </li>
              )
            })}
          </ul>
        </>
      )}
      {adding ? (
        <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); void submit() }}>
          <input autoFocus aria-label="New step" value={title} onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => { if (e.key === 'Escape') { setAdding(false); setTitle('') } }} placeholder="Add a step…" className="h-7 min-w-0 flex-1 rounded-control border border-border bg-bg px-2 text-xs focus-visible:bg-bg!" />
          <Act type="submit" primary>Add</Act>
        </form>
      ) : (
        <button type="button" data-add-step onClick={() => setAdding(true)} className={cn(ROW, 'text-fg-muted')}><Plus aria-hidden className="size-3" />Add a step<Kbd className="ml-auto">A</Kbd></button>
      )}
      <Err>{error}</Err>
      {plan?.task_ref && (
        <section className="flex flex-col gap-1.5">
          <span className={CAPTION}>Project</span>
          <div className={ROW}><Chip square>{plan.task_ref}</Chip><span className="min-w-0 flex-1 truncate">{plan.title ?? 'Linked work'}</span>{doing && <Chip>doing</Chip>}</div>
        </section>
      )}
    </>
  )
}

// ---------------------------------------------------------------- Topics
export function TopicsPanel({ marks }: { marks: Marks }) {
  if (!marks.topics.length) return <Empty>No topics yet. Add one from a message (hover it, then #). Agents add topics as the thread moves.</Empty>
  return (
    <>
      <span className={CAPTION}>Topics in this thread</span>
      <ol className="flex flex-col">
        {marks.topics.map((t, i) => (
          <li key={t.id}><button type="button" onClick={() => jump(t.message_ref)} className={cn(ROW, 'w-full text-left', i === 0 && 'bg-selection text-fg')}><span className="font-mono text-caption text-fg-muted">{t.seq ?? i + 1}</span><span className="min-w-0 flex-1 truncate">{t.title}</span><span className="text-label text-fg-muted">{time(t.created_at)}</span></button></li>
        ))}
      </ol>
      <p className="text-xs text-fg-muted">Click a topic to jump. Agents add topics as the thread moves.</p>
    </>
  )
}

// ---------------------------------------------------------------- Bookmarks
export function BookmarksPanel({ marks, messages, agent }: { marks: Marks; messages: ChatMessage[]; agent: string }) {
  if (!marks.bookmarks.length) return <Empty>No bookmarks yet. Hover a message and use the bookmark button (B).</Empty>
  return (
    <>
      <span className={CAPTION}>Bookmarked messages</span>
      <ul className="flex flex-col gap-2">
        {marks.bookmarks.map((b) => {
          const m = messages.find((x) => x.id === b.message_ref)
          return (
            <li key={b.id} className="group relative">
              <button type="button" onClick={() => jump(b.message_ref)} className="flex w-full flex-col gap-1 rounded-control border border-border p-3 text-left hover:bg-selection">
                <span className="text-label text-fg-muted">{m ? (m.from === 'operator' ? 'You' : agent) : 'Message'} · {time(m?.at ?? b.created_at)}</span>
                <span className="line-clamp-2 text-xs text-fg-secondary">{m?.body ?? b.note}</span>
              </button>
              <button type="button" aria-label="Remove bookmark" onClick={() => marks.toggleBookmark(b.message_ref)} className="absolute right-1.5 top-1.5 hidden size-5 place-items-center rounded-sm text-fg-muted hover:text-fg group-hover:grid group-focus-within:grid"><X aria-hidden className="size-3" /></button>
            </li>
          )
        })}
      </ul>
    </>
  )
}

// ---------------------------------------------------------------- Work
export function WorkPanel({ items, source, loading, error }: { items: WorkItem[]; source?: string; loading: boolean; error: string }) {
  if (loading) return <p role="status" className="text-xs text-fg-muted">Loading work…</p>
  const need = items.filter((i) => i.needs_user)
  const themes = items.filter((i) => i.kind === 'theme')
  const doing = items.filter((i) => i.kind === 'task' && i.status === 'doing')
  return (
    <>
      <div className="flex items-center gap-2"><span className={CAPTION}>Work · assigned to this agent</span><span className="flex-1" /><a href="/planner" className="no-underline"><Chip selected>Mini Planner</Chip></a></div>
      <Err>{error}</Err>
      {error ? null : source === 'stub' ? (
        <Empty>Work is not connected for this agent yet. Planner assignments appear here once it is.</Empty>
      ) : items.length === 0 ? (
        <Empty action={<a href="/planner" className="text-label text-fg-secondary underline">Open the full Planner</a>}>Nothing is assigned to this agent.</Empty>
      ) : (
        <>
          {need.length > 0 && (
            <div className="flex h-7 items-center gap-2 rounded-control border border-border px-2.5 text-xs text-fg-secondary"><StateDot state="blocked" />Need you · {need.length}<Chip square>decide {need.filter((n) => n.kind === 'decision').length}</Chip><Chip square>review {need.filter((n) => n.kind !== 'decision').length}</Chip></div>
          )}
          {themes.map((t, i) => (
            <div key={t.id} className={cn('flex flex-col gap-1 rounded-control px-2.5 py-2', i === 0 && 'bg-selection')}>
              <div className="flex items-center gap-2 text-control font-medium"><span className="min-w-0 flex-1 truncate">{t.title}</span>{t.needs_user && <StateDot state="blocked" />}</div>
              <SegmentedProgress size="sm" className="h-[3px]" label={`${t.title}: ${Math.round((t.progress ?? 0) * 100)}% done`} items={[{ state: 'done', count: Math.round((t.progress ?? 0) * 100) }, { state: 'todo', count: 100 - Math.round((t.progress ?? 0) * 100) }]} />
            </div>
          ))}
          {doing.length > 0 && (
            <section className="flex flex-col gap-1.5"><span className={CAPTION}>Doing</span>
              {doing.map((d) => <div key={d.id} className="flex flex-col gap-0.5 rounded-control border border-border px-2.5 py-2 text-xs"><span className="font-mono text-caption text-fg-muted">{d.id}</span>{d.title}</div>)}
            </section>
          )}
          <p className="text-xs text-fg-muted">Drag a card onto the chat to attach it · <a href="/planner" className="underline">open the full Planner</a></p>
        </>
      )}
    </>
  )
}

// ---------------------------------------------------------------- Activity
// KIT-GAP: @planner/shared EventTimeline is a dot-per-row facet feed; the chat golden's activity timeline has a rail,
// icon circles, day pills, quoted comments and mini cards. Built here until the kit has it.
const ICON: Record<string, typeof Zap> = { 'tool.call': Zap, 'card.sent': MessageSquare, 'card.answered': Check, 'approval.decided': ShieldCheck, 'file.shared': FileText, 'plan.changed': ClipboardCheck, 'task.closed': Check, 'panel.opened': ArrowUpRight, 'terminal.run': SquareTerminal, 'topic.added': Plus, 'bookmark.added': BookmarkIcon, comment: MessageSquare, note: ArrowUpRight }
const VERB: Record<string, string> = { 'tool.call': 'ran tools', 'card.sent': 'sent a card', 'card.answered': 'answered a card', 'approval.decided': 'decided an approval', 'file.shared': 'shared', 'plan.changed': 'changed the plan', 'task.closed': 'closed', 'panel.opened': 'opened a panel', 'terminal.run': 'ran a command', 'topic.added': 'added a topic', 'bookmark.added': 'bookmarked', comment: 'commented', note: 'noted' }
const actorName = (a: string) => (a === 'user' ? 'You' : a.replace(/^(agent|rule):/, '').replace(/-/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()))
const dayOf = (at?: number) => (at && new Date(at).toDateString() === new Date().toDateString() ? 'Today' : 'Earlier')
const SHOW = 8

export function ActivityPanel({ events, hasMore, loading, error, comment, more }: { events: Activity[]; hasMore: boolean; loading: boolean; error: string; comment: (b: string) => Promise<boolean>; more?: () => Promise<void> }) {
  const [q, setQ] = useState('')
  const [oldest, setOldest] = useState(false)
  const [kind, setKind] = useState('all')
  const [filters, setFilters] = useState(false)
  const [all, setAll] = useState(false)
  const [text, setText] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const shown = useMemo(() => {
    const f = events.filter((e) => (kind === 'all' || e.type === kind) && (!q || `${actorName(e.actor)} ${VERB[e.type] ?? e.type} ${e.payload?.title ?? ''} ${e.payload?.body ?? ''} ${e.cite?.map((c) => c.label).join(' ') ?? ''}`.toLowerCase().includes(q.toLowerCase())))
    return [...f].sort((a, b) => (oldest ? (a.at ?? a.seq) - (b.at ?? b.seq) : (b.at ?? b.seq) - (a.at ?? a.seq)))
  }, [events, q, oldest, kind])
  const visible = all ? shown : shown.slice(0, SHOW)
  const kinds = [...new Set(events.map((e) => e.type))]
  const post = async () => { const b = text.trim(); if (b && (await comment(b))) setText('') }
  let day = ''
  return (
    <>
      <div className="flex items-center gap-2">
        <label className="flex h-[26px] min-w-0 flex-1 items-center gap-1.5 rounded-control border border-border px-2 text-xs text-fg-muted"><Search aria-hidden className="size-3" /><input aria-label="Search activity" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search activity" className="min-w-0 flex-1 rounded-none border-0 bg-transparent p-0 text-xs focus-visible:bg-transparent!" /></label>
        <Act className="h-[26px]" aria-pressed={oldest} onClick={() => setOldest((o) => !o)}><ArrowUpDown aria-hidden className="size-3" />Sort</Act>
        <Act className="h-[26px]" aria-expanded={filters} onClick={() => setFilters((o) => !o)}><ListFilter aria-hidden className="size-3" />Filters</Act>
      </div>
      {filters && (
        <div role="toolbar" aria-label="Activity filters" className="flex flex-wrap gap-1.5">
          {['all', ...kinds].map((k) => <button key={k} type="button" aria-pressed={kind === k} onClick={() => setKind(k)}><Chip selected={kind === k} className="cursor-pointer">{k === 'all' ? 'All' : (VERB[k] ?? k)}</Chip></button>)}
        </div>
      )}
      {loading ? <p role="status" className="text-xs text-fg-muted">Loading activity…</p> : visible.length === 0 ? (
        <Empty>{events.length ? 'Nothing matches the search or filter.' : 'No activity yet. Tool calls, cards, files and approvals appear here as they happen.'}</Empty>
      ) : (
        <ol aria-label="Activity" className="relative flex flex-col before:absolute before:bottom-3.5 before:left-[11px] before:top-3.5 before:border-l before:border-border">
          {visible.map((e) => {
            const d = dayOf(e.at)
            const head = d !== day ? d : null
            day = d
            const Icon = ICON[e.type] ?? ArrowUpRight
            const bad = e.type === 'approval.decided' && /denied/i.test(e.payload?.summary ?? '')
            return (
              <li key={e.seq} className="flex flex-col">
                {head && <div className="flex items-center gap-2.5 pb-1 pt-3"><i aria-hidden className="grid size-[22px] place-items-center rounded-full border-2 border-bg-elevated bg-surface text-fg-secondary"><Search className="size-2.5" /></i><span className="inline-flex h-5 items-center rounded-full border border-border px-2.5 text-label text-fg-secondary">{head}</span></div>}
                <div className="relative flex items-start gap-2.5 py-1.5">
                  <i aria-hidden className={cn('z-[1] grid size-[22px] shrink-0 place-items-center rounded-full border-2 border-bg-elevated bg-surface text-fg-secondary', bad && 'text-danger')}><Icon className="size-3" /></i>
                  <div className="min-w-0 pt-0.5 text-xs leading-normal text-fg-secondary">
                    <span className={cn('font-medium text-fg', bad && 'text-danger')}>{actorName(e.actor)}</span> {VERB[e.type] ?? e.type}{' '}
                    {e.cite?.map((c) => <Chip key={c.ref} square className="mx-0.5 h-[18px]">{c.label} ↗</Chip>)}
                    <span className="text-fg-muted"> · {time(e.at)}</span>
                    {e.type === 'card.sent' && e.payload?.body && <blockquote className="mt-1.5 rounded-r-md border-l-2 border-fg-muted bg-selection px-2.5 py-1.5">{e.payload.body}</blockquote>}
                    {e.type === 'comment' && e.payload?.body && <blockquote className="mt-1.5 rounded-r-md border-l-2 border-fg-muted bg-selection px-2.5 py-1.5">{e.payload.body}</blockquote>}
                    {e.type === 'task.closed' && e.payload?.title && (
                      <div className="mt-1.5 flex max-w-[300px] flex-col gap-1 rounded-control border border-border bg-bg px-2.5 py-2">
                        <div className="flex items-center justify-between gap-2 text-fg"><b className="font-semibold">{e.payload.title}</b>{e.payload.cite?.[0] && <Chip square className="h-[18px]">{e.payload.cite[0].label} ↗</Chip>}</div>
                        <div className="flex items-center gap-2.5 font-mono text-caption text-fg-muted"><span>{e.payload.summary}</span><span className="flex-1" /><span className="text-label text-fg-secondary">Review ▸</span></div>
                      </div>
                    )}
                    {e.type !== 'card.sent' && e.type !== 'task.closed' && e.type !== 'comment' && e.payload?.summary && <span className="text-fg-muted"> · {e.payload.summary}</span>}
                  </div>
                </div>
              </li>
            )
          })}
          {(shown.length > SHOW && !all) || (all && hasMore) ? (
            <li className="flex items-center gap-2.5 py-1.5"><i aria-hidden className="z-[1] grid size-[22px] place-items-center rounded-full border-2 border-bg-elevated bg-surface text-fg-secondary"><ChevronDown className="size-3" /></i><button type="button" onClick={() => { if (!all) setAll(true); else void more?.() }} className="text-label text-fg-secondary hover:text-fg">{all ? 'Load older events' : `Show ${shown.length - SHOW} more events`}</button></li>
          ) : null}
        </ol>
      )}
      <Err>{error}</Err>
      <form className="mt-auto" onSubmit={(e) => { e.preventDefault(); void post() }}>
        <input ref={input} aria-label="Comment" value={text} onChange={(e) => setText(e.target.value)} placeholder="Comment, or type c · @ mention" className="h-[30px] w-full rounded-control border border-border bg-bg px-2.5 text-xs placeholder:text-fg-secondary focus-visible:bg-bg!" />
      </form>
    </>
  )
}

// ---------------------------------------------------------------- Approvals
function QueueItem({ block, commit, agent }: { block: Extract<StreamBlock, { kind: 'approval' }>; commit?: Commit; agent: string }) {
  const a = useAnswer<'approved' | 'always' | 'denied'>({ reversible: block.reversible, commit: commit && ((v) => commit(block, v)) })
  if (a.phase !== 'pending') return <div role="status" className="flex items-center gap-2 text-xs text-fg-secondary"><StateDot state="done" />{a.value === 'denied' ? 'Denied' : 'Allowed'}: {block.title}{a.undoLeft > 0 && <><span className="flex-1" /><Act onClick={a.undo}>Undo {a.undoLeft}s</Act></>}</div>
  return (
    <div className="flex flex-col overflow-hidden rounded-control border border-border">
      <div className="flex items-center gap-2 border-b border-border-subtle px-3 py-2"><Chip square>{block.tool}{block.count && block.count > 1 ? ` ×${block.count}` : ''}</Chip><span className="flex-1" /><span className="text-label text-fg-muted">{agent}</span></div>
      <div className="flex flex-col gap-2.5 p-3"><span className="text-xs">{block.title}</span>
        <div className="flex flex-wrap gap-2"><Act primary kbd="Y" onClick={() => a.answer('approved')}>Allow once</Act><Act danger kbd="N" onClick={() => a.answer('denied')}>Deny</Act></div>
        <Err>{a.error}</Err>
      </div>
    </div>
  )
}
export function ApprovalsPanel({ blocks, commit, agent, rules, revoke }: { blocks: StreamBlock[]; commit?: Commit; agent: string; rules: Rule[]; revoke: (id: string) => void }) {
  const open = blocks.filter((b): b is Extract<StreamBlock, { kind: 'approval' }> => b.kind === 'approval' && !b.answered)
  return (
    <>
      <span className={CAPTION}>Approval queue · {open.length} waiting</span>
      {open.length === 0 ? <Empty>Nothing is waiting. Approvals the agent asks for appear here.</Empty> : open.map((b) => <QueueItem key={b.id} block={b} commit={commit} agent={agent} />)}
      {rules.length > 0 && (
        <section className="flex flex-col gap-1.5"><span className={CAPTION}>Always allowed in this chat</span>
          {rules.map((r) => <div key={r.id} className={ROW}><Lock aria-hidden className="size-3 text-fg-muted" /><span className="min-w-0 flex-1 truncate font-mono text-caption">{r.tool}</span><button type="button" onClick={() => revoke(r.id)} className="text-label text-fg-secondary underline">Revoke</button></div>)}
        </section>
      )}
    </>
  )
}

// ---------------------------------------------------------------- Files
const DEMO_TREE = [
  { name: 'apps', depth: 0, open: true }, { name: 'tachyon', depth: 1, open: true }, { name: 'Chat.tsx', depth: 2, flag: 'M', hl: true }, { name: 'PlannerPage.tsx', depth: 2 }, { name: 'nanite', depth: 1, open: false }, { name: 'libs', depth: 0, open: false },
]
export function FilesPanel({ threadRef, demo, onOpenDoc }: { threadRef?: string; demo?: boolean; onOpenDoc: (i: WorkspaceItem) => void }) {
  const ws = useWorkspace(demo ? undefined : threadRef)
  const [filter, setFilter] = useState('')
  const items = ws.items.filter((i) => i.name.toLowerCase().includes(filter.toLowerCase()))
  return (
    <>
      <span className={CAPTION}>Files · {demo ? '~/dev/hollis-labs' : 'this session'}</span>
      <label className="flex h-7 items-center gap-2 rounded-control border border-border px-2.5 text-xs text-fg-muted"><input aria-label="Filter files" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter files" className="min-w-0 flex-1 rounded-none border-0 bg-transparent p-0 text-xs focus-visible:bg-transparent!" /></label>
      {demo ? (
        <div role="tree" aria-label="Files" className="flex flex-col">
          {DEMO_TREE.filter((n) => n.name.toLowerCase().includes(filter.toLowerCase())).map((n) => (
            <div key={n.name} role="treeitem" aria-selected={!!n.hl} style={{ paddingLeft: 6 + n.depth * 16 }} className={cn(ROW, n.hl && 'bg-selection text-fg')}>{n.open === undefined ? null : n.open ? <ChevronDown aria-hidden className="size-3" /> : <ChevronRight aria-hidden className="size-3" />}<span className="flex-1">{n.name}</span>{n.flag && <Chip square>{n.flag}</Chip>}</div>
          ))}
        </div>
      ) : ws.loading ? <p role="status" className="text-xs text-fg-muted">Loading files…</p> : items.length === 0 ? (
        <Empty>{ws.items.length ? 'No file matches.' : 'No files in this chat. Drop files into the Workspace to share them with the agent.'}</Empty>
      ) : (
        <ul className="flex flex-col">{items.map((i) => <li key={i.id}><button type="button" onClick={() => onOpenDoc(i)} className={cn(ROW, 'w-full text-left')}><FileText aria-hidden className="size-3 text-fg-muted" /><span className="min-w-0 flex-1 truncate">{i.name}</span>{i.shared && <Chip square>shared</Chip>}</button></li>)}</ul>
      )}
      <Err>{ws.error}</Err>
      <p className="text-xs text-fg-muted">Drag a file onto the composer to share it</p>
    </>
  )
}

// ---------------------------------------------------------------- Doc
export function DocPanel({ doc, onShare, onClose }: { doc: { name: string; text: string; itemId?: string; shared?: boolean } | null; onShare: () => void; onClose: () => void }) {
  if (!doc) return <Empty>No document is open. Open a file from Files, or the agent shares one here.</Empty>
  return (
    <>
      <div className="flex flex-wrap items-center gap-2"><span className={CAPTION}>Doc · full window</span><Chip square>{doc.name}</Chip><span className="flex-1" /><Act onClick={onShare} aria-pressed={!!doc.shared}>{doc.shared ? 'Shared with agent' : 'Share with agent'}</Act><Act kbd="Esc" onClick={onClose}>Close</Act></div>
      <article aria-label={doc.name} className="max-w-[70ch] self-center text-control leading-[1.7]"><MessageBody body={doc.text} /></article>
    </>
  )
}
