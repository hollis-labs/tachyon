import { Button, Sheet, SheetContent, SheetDescription, SheetTitle, Textarea } from '@hollis-labs/design-components'
import { X } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { CommentCheck, ReviewValidation, ReviewVerdict } from '../types'
import { LABEL } from './Comments'
import type { PendingComment, WriteResult } from './useWorkspace'

const OPTIONS: { value: ReviewVerdict; label: string; hint: string }[] = [
  { value: 'comment', label: 'Comment', hint: 'General feedback without an explicit approval.' },
  { value: 'approve', label: 'Approve', hint: 'Approve these changes.' },
  { value: 'request_changes', label: 'Request changes', hint: 'Feedback must be addressed before merging.' },
]

export interface ReviewPanelProps {
  /** Short PR reference shown beside the title, e.g. tether#507. */
  prLabel: string
  pending: PendingComment[]
  onSubmit: (v: { verdict: ReviewVerdict; body?: string }) => Promise<WriteResult>
  onUnqueue: (id: string) => void
  /** Checks queued comments against the current head; omitted when the forge has no validate route. */
  validate?: (comments: PendingComment[]) => Promise<ReviewValidation>
  onClose: () => void
}

export function ReviewPanel({ prLabel, pending, onSubmit, onUnqueue, validate, onClose }: ReviewPanelProps) {
  const [verdict, setVerdict] = useState<ReviewVerdict>('comment')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [check, setCheck] = useState<ReviewValidation | null>(null)
  useEffect(() => {
    setCheck(null)
    if (!validate || pending.length === 0) return
    let live = true
    validate(pending).then((r) => { if (live) setCheck(r) }, () => undefined)
    return () => { live = false }
  }, [validate, pending])
  const status = (i: number): CommentCheck => check?.statuses[i] ?? 'ok'
  const invalid = pending.some((_, i) => status(i) === 'invalid_line')
  const needsText = verdict === 'request_changes' && !body.trim() && pending.length === 0
  const emptyComment = verdict === 'comment' && !body.trim() && pending.length === 0
  const blocked = needsText || emptyComment || invalid || check?.headMoved === true

  const submit = async () => {
    setBusy(true)
    setError(null)
    const r = await onSubmit({ verdict, body: body.trim() || undefined })
    setBusy(false)
    if (r.ok) onClose()
    else setError(r.message ?? 'Review not submitted')
  }

  return (
    <Sheet open onOpenChange={(open) => { if (!open && !busy) onClose() }}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md" aria-label="Submit review" data-testid="forge-review-panel">
        <header className="flex items-baseline gap-2 border-b border-border px-4 py-3 pr-12">
          <SheetTitle className="font-sans text-control font-semibold normal-case tracking-normal text-fg">Review</SheetTitle>
          <SheetDescription className="font-mono text-caption text-fg-muted">{prLabel}</SheetDescription>
        </header>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
          <fieldset className="space-y-2" disabled={busy}>
            <legend className="sr-only">Review verdict</legend>
            {OPTIONS.map((o) => (
              <label key={o.value} className={`flex cursor-pointer items-start gap-3 rounded border border-border px-3 py-2.5 ${verdict === o.value ? 'bg-surface-hover' : 'hover:bg-surface/40'}`}>
                <input type="radio" name="verdict" value={o.value} checked={verdict === o.value} onChange={() => setVerdict(o.value)} className="mt-1 accent-primary" />
                <span className="min-w-0">
                  <span className="block text-control font-medium text-fg">{o.label}</span>
                  <span className="block text-xs text-fg-muted">{o.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>
          <Textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={4}
            placeholder="Review summary (required with queued comments off)…"
            aria-label="Review summary"
            disabled={busy}
            className="w-full resize-y text-control"
          />
          {pending.length > 0 && (
            <div>
              <h3 className={`${LABEL} mb-2`}>Queued inline comments · {pending.length}</h3>
              <ul className="divide-y divide-border rounded border border-border">
                {pending.map((p, i) => (
                  <li key={p.id} className="flex items-start gap-2 px-3 py-2">
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-xs font-medium text-fg">{p.path}:{p.line}</span>
                      <span className="block text-xs text-fg-muted">{p.body}</span>
                      {status(i) === 'outdated' && <span className="block text-xs text-fg-muted">Outdated: this line moved since you wrote it.</span>}
                      {status(i) === 'invalid_line' && <span className="block text-xs text-danger">This line is no longer in the diff. Discard it to submit.</span>}
                    </span>
                    <Button variant="outline" size="icon-xs" aria-label={`Discard comment on ${p.path}:${p.line}`} onClick={() => onUnqueue(p.id)}><X className="size-3" /></Button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {check?.headMoved && <p className="text-xs text-danger" role="alert">The PR changed. Refresh to review again; your drafts are kept.</p>}
          {needsText && <p className="text-xs text-fg-muted">Requesting changes needs a summary or at least one inline comment.</p>}
          {error && <p className="text-xs text-danger" role="alert">{error}</p>}
          <p className="text-xs text-fg-muted">Posts to the forge as the token owner. Drafts and queued comments are kept if you close this panel.</p>
        </div>
        <footer className="flex items-center justify-between gap-2 border-t border-border px-4 py-3">
          <Button variant="outline" size="sm" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button size="sm" onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Submitting…' : 'Submit review'}</Button>
        </footer>
      </SheetContent>
    </Sheet>
  )
}
