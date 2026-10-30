import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@hollis-labs/design-components'
import { useState } from 'react'
import { Btn } from '../../agents/ui'
import type { ReviewVerdict } from '../types'
import { LABEL } from './Comments'
import type { PendingComment, WriteResult } from './useWorkspace'

const OPTIONS: { value: ReviewVerdict; label: string; hint: string }[] = [
  { value: 'comment', label: 'Comment', hint: 'General feedback without an explicit approval.' },
  { value: 'approve', label: 'Approve', hint: 'Approve these changes.' },
  { value: 'request_changes', label: 'Request changes', hint: 'Feedback must be addressed before merging.' },
]

export interface ReviewPanelProps {
  pending: PendingComment[]
  onSubmit: (v: { verdict: ReviewVerdict; body?: string }) => Promise<WriteResult>
  onUnqueue: (id: string) => void
  onClose: () => void
}

export function ReviewPanel({ pending, onSubmit, onUnqueue, onClose }: ReviewPanelProps) {
  const [verdict, setVerdict] = useState<ReviewVerdict>('comment')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const needsText = verdict === 'request_changes' && !body.trim() && pending.length === 0
  const emptyComment = verdict === 'comment' && !body.trim() && pending.length === 0
  const blocked = needsText || emptyComment

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
    <SheetContent side="right" className="flex w-full flex-col gap-0 overflow-y-auto p-4 sm:max-w-md" aria-label="Submit review" data-testid="forge-review-panel">
      <SheetTitle className="mb-1 font-sans text-base font-semibold normal-case tracking-normal text-fg">Finish your review</SheetTitle>
      <SheetDescription className="mb-3 dash-support text-fg-muted">Inline comments you queued are kept if you close this and come back.</SheetDescription>
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        rows={3}
        placeholder="Leave a review summary…"
        aria-label="Review summary"
        disabled={busy}
        className="mb-3 w-full resize-y border border-border bg-bg px-2 py-1.5 text-[13px] text-fg placeholder:text-fg-faint focus-visible:border-fg focus-visible:outline-none"
      />
      <fieldset className="mb-3 space-y-1.5" disabled={busy}>
        <legend className="sr-only">Review verdict</legend>
        {OPTIONS.map((o) => (
          <label key={o.value} className="flex cursor-pointer items-start gap-2 text-[13px]">
            <input type="radio" name="verdict" value={o.value} checked={verdict === o.value} onChange={() => setVerdict(o.value)} className="mt-1 accent-primary" />
            <span>
              <span className="text-fg">{o.label}</span>
              <span className="ml-2 text-[12px] text-fg-faint">{o.hint}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {pending.length > 0 && (
        <div className="mb-3">
          <p className={`${LABEL} mb-1`}>{pending.length} pending inline comment{pending.length === 1 ? '' : 's'}</p>
          <ul className="divide-y divide-border border-y border-border">
            {pending.map((p) => (
              <li key={p.id} className="flex items-baseline gap-2 py-1.5 text-[12px]">
                <span className="shrink-0 font-mono text-fg-faint">{p.path}:{p.line}</span>
                <span className="min-w-0 flex-1 truncate text-fg">{p.body}</span>
                <button type="button" onClick={() => onUnqueue(p.id)} className="font-mono text-[10px] uppercase tracking-wider text-fg-faint hover:text-danger">Discard</button>
              </li>
            ))}
          </ul>
        </div>
      )}
      {needsText && <p className="mb-2 text-[12px] text-fg-muted">Requesting changes needs a summary or at least one inline comment.</p>}
      {error && <p className="mb-2 text-[12px] text-danger" role="alert">{error}</p>}
      <div className="flex items-center gap-2">
        <span className="mr-auto font-mono text-[10px] text-fg-faint">You will be asked to confirm your identity.</span>
        <Btn onClick={onClose} disabled={busy}>Cancel</Btn>
        <Btn tone="primary" onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Submitting…' : 'Submit review'}</Btn>
      </div>
    </SheetContent>
    </Sheet>
  )
}
