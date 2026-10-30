import { useState } from 'react'
import { Button, Textarea } from '@hollis-labs/design-components'
import { TEXT } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { notifyError } from '@planner/api/notifications'
import { usePlannerApi } from '@planner/api/context'
import type { PlannerComment, ThemeRow } from '@planner/api/types'

const heading = 'font-mono dash-caption uppercase tracking-label text-fg-faint'

/** Links, Decisions, Activity and the composer for the open theme. Plugin tabs append sections. */
export function Context({ row, comments, onCommented, mentionable, links }: { row: ThemeRow; comments: readonly PlannerComment[]; onCommented: () => void; mentionable: readonly { id: string; title: string }[]; links: readonly { kind: string; label: string }[] }) {
  const api = usePlannerApi()
  const extra = useContributions('planner.context.tab')
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const [at, setAt] = useState(0)
  const [menuOff, setMenuOff] = useState(false)
  // An "@" word at the end of the text opens the mention menu.
  const query = /(?:^|\s)@([\w-]*)$/.exec(text)?.[1]
  const matches = query === undefined || menuOff ? [] : mentionable.filter((m) => `${m.id} ${m.title}`.toLowerCase().includes(query.toLowerCase())).slice(0, 8)
  const mention = (m: { id: string }) => { setText((t) => t.replace(/@[\w-]*$/, `@${m.id} `)); setAt(0) }
  const send = async () => {
    const body = text.trim()
    if (!body || sending) return
    setSending(true)
    try {
      await api.plannerComment('themes', row.id, body, row.rev)
      setText('')
      onCommented()
    } catch (err) {
      notifyError(err, TEXT.writeFailed) // the text stays in the field
    } finally {
      setSending(false)
    }
  }
  return (
    <aside aria-label={TEXT.detailsLabel} className="flex min-h-0 flex-col gap-5 overflow-y-auto p-4">
      <section aria-labelledby="ctx-links" className="flex flex-col gap-1.5">
        <h2 id="ctx-links" className={heading}>{TEXT.links}</h2>
        {links.length === 0 ? <p className="dash-support text-fg-muted">{TEXT.linksEmpty}</p> : (
          <ul className="flex flex-col gap-1">
            {links.map((l) => <li key={l.label} className="dash-support text-fg"><span className="mr-2 dash-caption text-fg-faint">{l.kind}</span>{l.label}</li>)}
          </ul>
        )}
      </section>
      <section aria-labelledby="ctx-decisions" className="flex flex-col gap-1.5">
        <h2 id="ctx-decisions" className={heading}>{TEXT.decisions}</h2>
        {(row.pending_decisions ?? []).length === 0 ? <p className="dash-support text-fg-muted">{TEXT.decisionsEmpty}</p> : (
          <ul className="flex flex-col gap-1">{row.pending_decisions!.map((d) => <li key={d.id} className="dash-support text-fg">{d.title}</li>)}</ul>
        )}
      </section>
      {extra.map((c) => <section key={c.id} aria-label={c.label}>{c.render?.({ id: row.id })}</section>)}
      <section aria-labelledby="ctx-activity" className="flex flex-col gap-1.5">
        <h2 id="ctx-activity" className={heading}>{TEXT.activity}</h2>
        {comments.length === 0 ? <p className="dash-support text-fg-muted">{TEXT.activityEmpty}</p> : (
          <ul className="flex flex-col gap-2">
            {comments.slice(-8).reverse().map((c) => (
              <li key={c.id} className="dash-support text-fg"><span className="mr-2 dash-caption text-fg-faint">{c.author}</span>{c.text}</li>
            ))}
          </ul>
        )}
      </section>
      <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); void send() }}>
        <Textarea
          aria-label={TEXT.commentPlaceholder}
          placeholder={TEXT.commentPlaceholder}
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
        />
        {matches.length > 0 && (
          <ul role="listbox" aria-label={TEXT.mentionLabel} className="flex flex-col rounded-md border border-border bg-panel">
            {matches.map((m, i) => (
              <li key={m.id} role="option" aria-selected={i === at} onMouseDown={(e) => { e.preventDefault(); mention(m) }} className="cursor-pointer px-2 py-1 dash-support text-fg aria-selected:bg-panel-hover">
                <span className="mr-2 font-mono dash-caption text-fg-faint">{m.id}</span>{m.title}
              </li>
            ))}
          </ul>
        )}
        <Button type="submit" size="sm" disabled={sending || !text.trim()}>{TEXT.commentSend}</Button>
      </form>
    </aside>
  )
}
