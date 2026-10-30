import { useState, type ReactNode } from 'react'
import { ago } from '@console/lib/agent-activity'
import { Linkified } from '../../autolink/Linkified'
import { Button } from '@hollis-labs/design-components'
import { Chip } from '../../components/chip'
import { InitialsAvatar } from '../../components/initials-avatar'
import type { ForgeComment, ForgeUser, ReviewComment } from '../types'
import type { Thread } from './diff-utils'

export const LABEL = 'font-mono text-caption uppercase tracking-label text-fg-muted'

export function Badge({ children, danger }: { children: ReactNode; danger?: boolean }) {
  return <Chip tone={danger ? 'danger' : 'neutral'}>{children}</Chip>
}

export function Author({ user }: { user: ForgeUser }) {
  return (
    <span className="text-control font-medium text-fg">
      {user.name ?? user.login}
      {user.isAgent && <span className="ml-1 font-normal text-fg-faint">· agent</span>}
    </span>
  )
}

export function CommentItem({ c, posting, onReply, where }: { c: ForgeComment; posting?: boolean; onReply?: () => void; where?: string }) {
  const [now] = useState(() => Date.now())
  return (
    <article className={`flex gap-3 px-3 py-2.5 ${posting ? 'opacity-60' : ''}`} data-testid="forge-comment" aria-busy={posting || undefined}>
      <InitialsAvatar name={c.author.name ?? c.author.login} className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <header className="flex flex-wrap items-center gap-x-2">
          <Author user={c.author} />
          {where && <span className="text-xs text-fg-muted">{where}</span>}
          <span className="text-xs text-fg-muted">{where ? '· ' : ''}{posting ? 'posting…' : ago(Date.parse(c.createdAt), now)}</span>
          {onReply && !posting && <Button variant="outline" size="xs" className="ml-auto" onClick={onReply}>Reply</Button>}
        </header>
        {/* Body is untrusted forge text: rendered as text only, with ref chips. */}
        <div className="mt-1 text-control leading-relaxed text-fg [overflow-wrap:anywhere]">
          <Linkified text={c.body} preserveWhitespace />
        </div>
      </div>
    </article>
  )
}

export interface ComposerProps {
  placeholder: string
  submitLabel: string
  /** Extra secondary action (e.g. "Add to review"). Clears the draft on call. */
  secondary?: { label: string; run: (body: string) => void }
  onSubmit: (body: string) => Promise<{ ok: boolean; message?: string }>
  onCancel?: () => void
  autoFocus?: boolean
  disabled?: boolean
}

/** Draft is kept until the write succeeds, so a failed post never loses the text. */
export function Composer({ placeholder, submitLabel, secondary, onSubmit, onCancel, autoFocus, disabled }: ComposerProps) {
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const empty = !body.trim()
  const submit = async () => {
    if (empty || busy) return
    setBusy(true)
    setError(null)
    const res = await onSubmit(body)
    setBusy(false)
    if (res.ok) setBody('')
    else setError(res.message ?? 'Could not post')
  }
  return (
    <div className="space-y-2">
      <textarea
        value={body}
        autoFocus={autoFocus}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void submit()
          if (e.key === 'Escape' && onCancel) onCancel()
        }}
        rows={3}
        placeholder={placeholder}
        aria-label={placeholder}
        disabled={busy || disabled}
        className="w-full resize-y rounded border border-border bg-bg px-3 py-2 font-sans text-control text-fg placeholder:text-fg-faint focus-visible:border-fg focus-visible:outline-none disabled:opacity-60"
      />
      {error && <p className="text-xs text-danger" role="alert">{error}. Your draft is kept; try again.</p>}
      <div className="flex items-center gap-2">
        <span className="mr-auto text-caption text-fg-faint">Ctrl+Enter to post</span>
        {onCancel && <Button variant="outline" size="sm" onClick={onCancel} disabled={busy}>Cancel</Button>}
        {secondary && <Button variant="outline" size="sm" onClick={() => { if (!empty) { secondary.run(body); setBody('') } }} disabled={empty || busy || disabled}>{secondary.label}</Button>}
        <Button size="sm" onClick={() => void submit()} disabled={empty || busy || disabled}>{busy ? 'Posting…' : submitLabel}</Button>
      </div>
    </div>
  )
}

export interface ThreadViewProps {
  thread: Thread
  posting: ForgeComment[]
  readOnly: boolean
  onReply: (inReplyTo: string, body: string) => Promise<{ ok: boolean; message?: string }>
  showPath?: boolean
}

export function ThreadView({ thread, posting, readOnly, onReply, showPath }: ThreadViewProps) {
  const [replying, setReplying] = useState(false)
  const [open, setOpen] = useState(!(thread.outdated || thread.resolved))
  const mine = posting.filter((p): p is ReviewComment => p.kind === 'review' && p.inReplyTo === thread.id)
  const loc = thread.root.line != null ? `${showPath ? `${thread.path}:` : 'line '}${thread.root.line}${thread.root.side === 'left' ? ' (old)' : ''}` : thread.path
  return (
    <section className="mx-3 my-2 rounded border border-border bg-surface font-sans" data-testid="forge-thread" aria-label={`Comment thread on ${loc}`}>
      <header className="flex flex-wrap items-center gap-2 px-3 py-1.5">
        <span className="font-mono text-caption text-fg-muted">{loc}</span>
        {thread.outdated && <Badge>Outdated</Badge>}
        {thread.resolved && <Badge>Resolved</Badge>}
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          className="ml-auto text-xs text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {open ? 'Collapse' : `Show ${thread.replies.length + 1}`}
        </button>
      </header>
      {open && (
        <>
          <div className="divide-y divide-border">
            <CommentItem c={thread.root} />
            {thread.replies.map((r) => <CommentItem key={r.id} c={r} />)}
            {mine.map((p) => <CommentItem key={p.id} c={p} posting />)}
          </div>
          {!readOnly && (
            <div className="border-t border-border px-3 py-2">
              {replying ? (
                <Composer
                  autoFocus
                  placeholder="Reply…"
                  submitLabel="Reply"
                  onCancel={() => setReplying(false)}
                  onSubmit={async (b) => {
                    const r = await onReply(thread.id, b)
                    if (r.ok) setReplying(false)
                    return r
                  }}
                />
              ) : (
                <Button variant="outline" size="xs" onClick={() => setReplying(true)}>Reply</Button>
              )}
            </div>
          )}
        </>
      )}
    </section>
  )
}
