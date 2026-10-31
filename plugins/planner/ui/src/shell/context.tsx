import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Pin } from 'lucide-react'
import { RefChip } from '@console/autolink'
import { Button, cn } from '@hollis-labs/design-components'
import { TEXT } from '@planner/lib/vocabulary'
import { boardAge } from '@planner/lib/planner'
import { useContributions } from '@planner/lib/regions'
import { authorizing, notifyError } from '@planner/api/notifications'
import { usePlannerApi } from '@planner/api/context'
import { CAPTION, Kbd, LinkDropZone, StateDot, StatusMenu, type StatusMenuOption } from '@planner/shared'
import type { PlannerComment, PlannerDb, PlannerItem } from '@planner/api/types'

export interface ContextLink {
  kind: string
  label: string
  /** Right-aligned note: "blocks", "open". */
  note?: string
  /** An absolute or ~/ path of a file link: the row opens it in the shared file viewer. */
  path?: string
}

export interface ContextDecision {
  id: string
  title: string
}

export interface ContextProps {
  db: PlannerDb
  id: string
  rev: number
  pinned: boolean
  comments: readonly PlannerComment[]
  links: readonly ContextLink[]
  decisions?: readonly ContextDecision[]
  onCommented: () => void
  /** `ref` is the short handle shown beside the title ("#4"); the id is the fallback. */
  mentionable: readonly { id: string; title: string; ref?: string }[]
  /** Status changes offered from the composer; a chosen one is applied when the comment is sent. */
  statusOptions?: readonly StatusMenuOption[]
  onStatus?: (value: string) => Promise<void>
  /** The composer hint names this: "a task or theme", "an item". */
  children?: ReactNode
}

const Section = ({ id, title, children, grow }: { id: string; title: ReactNode; children: ReactNode; grow?: boolean }) => (
  <section aria-labelledby={id} className={cn('flex flex-col gap-2 border-b border-border-subtle p-3', grow && 'flex-1 border-b-0')}>
    <h2 id={id} className={CAPTION}>{title}</h2>
    {children}
  </section>
)

/** Text with "@words" shown as mention chips. */
function Mentions({ text }: { text: string }) {
  return <>{text.split(/(@[\w.-]+)/g).map((part, i) => (part.startsWith('@') ? <span key={i} className="rounded-sm bg-info/12 px-1 text-fg">{part}</span> : part))}</>
}

const who = (author: string) => (author.startsWith('operator') ? TEXT.you : author)

/** Links, open Decisions, Activity and the composer for the open item. Plugin tabs append sections. */
export function Context({ db, id, rev, pinned: pinnedNow, comments, links, decisions = [], onCommented, mentionable, statusOptions, onStatus }: ContextProps) {
  const api = usePlannerApi()
  const extra = useContributions('planner.context.tab')
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const [at, setAt] = useState(0)
  const [menuOff, setMenuOff] = useState(false)
  // Set only when the person changes it, so a plain comment never touches the pin.
  const [pinChange, setPinChange] = useState<boolean | undefined>(undefined)
  const [status, setStatus] = useState<string | undefined>(undefined)
  useEffect(() => { setPinChange(undefined); setStatus(undefined) }, [id])
  const pinned = pinChange ?? pinnedNow
  // An "@" word at the end of the text opens the mention menu.
  const query = /(?:^|\s)@([\w-]*)$/.exec(text)?.[1]
  const matches = query === undefined || menuOff ? [] : mentionable.filter((m) => m.id !== id && `${m.id} ${m.title}`.toLowerCase().includes(query.toLowerCase())).slice(0, 8)
  const mention = (m: { id: string }) => { setText((t) => t.replace(/@[\w-]*$/, `@${m.id} `)); setAt(0) }
  const send = async () => {
    const body = text.trim()
    if (!body || sending) return
    setSending(true)
    try {
      await authorizing(() => api.plannerComment(db, id, body, rev, undefined, pinChange === undefined ? undefined : { pin: pinChange }))
      if (status && onStatus) await onStatus(status)
      setText('')
      setPinChange(undefined)
      setStatus(undefined)
      onCommented()
    } catch (err) {
      notifyError(err, TEXT.writeFailed) // the text stays in the field
    } finally {
      setSending(false)
    }
  }
  const now = Date.now()
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      <Section id="ctx-links" title={TEXT.links}>
        {links.length > 0 && (
          <ul className="flex flex-col">
            {links.map((l) => (
              <li key={`${l.kind}:${l.label}`} className="-mx-1.5 flex items-center gap-2 rounded-sm px-1.5 py-1 text-xs text-fg-secondary hover:bg-selection">
                <span className="w-14 shrink-0 font-mono text-caption text-fg-muted">{l.kind}</span>
                <StateDot state="todo" className="shrink-0" />
                {l.path
                  ? <span className="min-w-0 flex-1 truncate"><RefChip kind="path" canonical={l.path} fallbackLabel={l.label} /></span>
                  : <span className="min-w-0 flex-1 truncate">{l.label}</span>}
                {l.note && <span className="shrink-0 text-label text-fg-muted">{l.note}</span>}
              </li>
            ))}
          </ul>
        )}
        {links.length === 0 && <p className="text-xs text-fg-muted">{TEXT.linksEmpty}</p>}
        <LinkDropZone id={id}>{TEXT.linkDrop} <Kbd>@</Kbd> {TEXT.linkDropTail}</LinkDropZone>
      </Section>
      <Section id="ctx-decisions" title={`${TEXT.decisions}${decisions.length ? ` · ${TEXT.openCount(decisions.length)}` : ''}`}>
        {decisions.length === 0 ? <p className="text-xs text-fg-muted">{TEXT.decisionsEmpty}</p> : decisions.map((d) => <PendingDecision key={d.id} decision={d} onDone={onCommented} />)}
      </Section>
      {extra.map((c) => <section key={c.id} aria-label={c.label} className="border-b border-border-subtle p-3">{c.render?.({ id })}</section>)}
      <Section id="ctx-activity" title={TEXT.activity} grow>
        {comments.length === 0 ? <p className="text-xs text-fg-muted">{TEXT.activityEmpty}</p> : (
          <ul className="flex flex-col gap-2">
            {comments.slice(-8).reverse().map((c) => (
              <li key={c.id} className="flex flex-col gap-0.5 text-xs leading-snug">
                <span className="font-mono text-caption text-fg-muted">{who(c.author)} · {boardAge(c.created, now)}</span>
                <span className="text-fg"><Mentions text={c.text} /></span>
              </li>
            ))}
          </ul>
        )}
      </Section>
      <form className="relative m-3 mt-auto flex flex-col gap-2 rounded-panel border border-border bg-bg p-2 text-xs" onSubmit={(e) => { e.preventDefault(); void send() }}>
        <textarea
          aria-label={TEXT.commentPlaceholder}
          placeholder={TEXT.commentPlaceholder}
          rows={2}
          value={text}
          onChange={(e) => { setText(e.target.value); setMenuOff(false) }}
          onKeyDown={(e) => {
            if (matches.length > 0) {
              if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); setAt((i) => (i + (e.key === 'ArrowDown' ? 1 : matches.length - 1)) % matches.length); return }
              if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); mention(matches[at] ?? matches[0]); return }
              if (e.key === 'Escape') { e.stopPropagation(); setMenuOff(true); return }
            }
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void send() }
          }}
          className="min-h-9 w-full resize-none bg-transparent px-0.5 text-xs text-fg outline-none placeholder:text-fg-muted"
        />
        {matches.length > 0 && (
          <div className="absolute inset-x-2 bottom-full z-20 mb-1 rounded-panel border border-surface-hover bg-bg-elevated p-1 shadow-lg">
            <p className={cn('px-2 pb-1 pt-1.5', CAPTION)}>{TEXT.linkToHeading}</p>
            <ul role="listbox" aria-label={TEXT.mentionLabel} className="flex flex-col">
              {matches.map((m, i) => (
                <li key={m.id} role="option" aria-selected={i === at} onMouseDown={(e) => { e.preventDefault(); mention(m) }} className="flex h-7 cursor-pointer items-center gap-2 rounded-sm px-2 text-control text-fg-secondary aria-selected:bg-selection aria-selected:text-fg">
                  <StateDot state="doing" />
                  <span className="truncate"><span className="mr-1.5 font-mono text-caption text-fg-muted">{m.ref ?? (m.id.length > 12 ? m.id.slice(0, 8) : m.id)}</span>{m.title}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        <div className="flex items-center gap-1.5 text-label text-fg-faint">
          <button type="button" onClick={() => { setText((t) => `${t}${t && !/\s$/.test(t) ? ' ' : ''}@`); setMenuOff(false) }} className="rounded-sm px-1 hover:text-fg-secondary">@ {TEXT.linkVerb}</button>
          {statusOptions && onStatus && (
            <>
              <span aria-hidden>·</span>
              <StatusMenu trigger={<span className="rounded-sm px-1 hover:text-fg-secondary">{status ? `→ ${statusOptions.find((o) => o.value === status)?.label ?? status}` : TEXT.statusVerb}</span>} triggerLabel={TEXT.statusVerb} heading={TEXT.moveTo} current={status ?? ''} options={statusOptions} onSelect={setStatus} />
            </>
          )}
          <span aria-hidden>·</span>
          <button type="button" aria-pressed={pinned} aria-label={TEXT.pinWithComment} onClick={() => setPinChange(pinned ? false : true)} className={cn('rounded-sm px-1 hover:text-fg-secondary', pinned && 'text-fg')}>
            <Pin className="size-3" />
          </button>
          <span className="flex-1" />
          <button type="submit" disabled={sending || !text.trim()} aria-label={TEXT.commentSend} className="rounded-sm disabled:opacity-50"><Kbd>⌘↵</Kbd></button>
        </div>
      </form>
    </div>
  )
}

/** An open decision with its options as buttons, and Defer. Choosing records it with Torque's decision write. */
function PendingDecision({ decision, onDone }: { decision: ContextDecision; onDone: () => void }) {
  const api = usePlannerApi()
  const [item, setItem] = useState<PlannerItem | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let live = true
    api.plannerGet('decisions', decision.id).then((i) => live && setItem(i), () => live && setItem(null))
    return () => { live = false }
  }, [api, decision.id])
  const run = async (fn: () => Promise<unknown>) => {
    if (busy) return
    setBusy(true)
    try {
      await authorizing(fn)
      onDone()
    } catch (err) {
      notifyError(err, TEXT.writeFailed)
    } finally {
      setBusy(false)
    }
  }
  const options = item?.options ?? []
  return (
    <div className="flex flex-col gap-2">
      <Link to={`/planner/decisions/${encodeURIComponent(decision.id)}`} className="text-xs leading-snug text-fg hover:underline">{decision.title}</Link>
      {item && (
        <div className="flex flex-wrap gap-1.5">
          {options.slice(0, 3).map((o) => <Button key={o.id} size="sm" variant="outline" disabled={busy} onClick={() => void run(() => api.plannerDecide(item.id, o.id, item.rev))}>{o.label}</Button>)}
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void run(() => api.plannerDefer(item.id, item.rev))}>{TEXT.defer}</Button>
        </div>
      )}
    </div>
  )
}
