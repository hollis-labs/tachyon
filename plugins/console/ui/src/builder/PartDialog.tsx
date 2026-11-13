import { useState } from 'react'
import { Button, Dialog, DialogContent, DialogDescription, DialogTitle, Input, Textarea } from '@hollis-labs/design-components'
import { type Change, builderApi, errorMessage } from './api'
import { type PartKind, type ReviewRow, newRequestId } from './changes'
import { ReviewDialog } from './ReviewDialog'

const SLUG = /^[a-z0-9][a-z0-9._-]{0,63}$/
const NOUN: Record<PartKind, string> = { prompts: 'prompt', policies: 'policy' }

export interface PartSeed {
  name: string
  summary: string
  /** Prompt text (prompts only). */
  body?: string
  /** Policy fields carried over when duplicating (policies only). */
  bundle?: Record<string, unknown>
}

/**
 * Create or duplicate one Library part (prompt or policy). Never saves by itself: it asks the backend to compute the
 * change and hands it to the Review dialog, so the save mode decides what happens. A new policy starts without
 * settings; they are edited on the policy once it exists.
 */
export function PartDialog({ kind, seed, duplicateOf, onClose, onApplied }: {
  kind: PartKind
  seed?: PartSeed
  duplicateOf?: string
  onClose: () => void
  onApplied: (id: string) => void
}) {
  const noun = NOUN[kind]
  const [id, setId] = useState('')
  const [name, setName] = useState(seed?.name ?? '')
  const [summary, setSummary] = useState(seed?.summary ?? '')
  const [body, setBody] = useState(seed?.body ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [review, setReview] = useState<{ change: Change; rows: ReviewRow[] } | null>(null)
  const idOk = SLUG.test(id)
  const carried = Object.keys(seed?.bundle ?? {})
  const submit = async () => {
    setBusy(true)
    setError('')
    const value = { name: name.trim(), summary: summary.trim(), ...(kind === 'prompts' ? { body } : { bundle: seed?.bundle ?? {} }) }
    const rows: ReviewRow[] = [
      { label: 'New', from: 'does not exist', to: id },
      { label: 'Name', from: 'empty', to: value.name },
      ...(value.summary ? [{ label: 'Summary', from: 'empty', to: value.summary }] : []),
      ...(kind === 'prompts' ? [{ label: 'Body', from: 'empty', to: body.trim() ? `${body.length} characters` : 'empty' }] : []),
      ...(carried.length ? [{ label: 'Settings', from: 'empty', to: `copied from ${duplicateOf}: ${carried.join(', ')}` }] : []),
    ]
    try {
      const change = await builderApi.createChange({
        request_id: newRequestId(),
        ops: [{ target: { kind: 'part', id, part_kind: kind === 'prompts' ? 'prompt' : 'policy' }, op: { type: 'create', value } }],
      })
      setReview({ change, rows })
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  if (review)
    return (
      <ReviewDialog
        initial={review.change}
        subject={id}
        rows={review.rows}
        onClose={() => setReview(null)}
        onApplied={() => {
          onApplied(id)
          onClose()
        }}
        onReload={() => setReview(null)}
      />
    )
  const cap = 'font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-2'
  const row = 'grid gap-1 sm:grid-cols-[130px_1fr] sm:gap-3'
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent widthClassName="max-w-xl" className="gap-3 max-sm:-mx-4 max-sm:-mb-4 max-sm:max-h-[88vh] max-sm:w-[calc(100%+2rem)] max-sm:max-w-none max-sm:self-end max-sm:overflow-auto max-sm:rounded-b-none max-sm:border-b-0">
        <DialogTitle className="font-sans text-base font-semibold normal-case tracking-normal">{duplicateOf ? `Duplicate ${noun} · ${duplicateOf}` : `New ${noun}`}</DialogTitle>
        <DialogDescription className="text-xs text-fg-muted">Nothing changes until you review the change and it is applied.</DialogDescription>
        <div className="flex flex-col gap-2">
          <label className={row}>
            <span className={cap}>Id</span>
            <span className="flex flex-col gap-1">
              <Input aria-label="Id" value={id} placeholder="lower-case-slug" onChange={(e) => setId(e.target.value.trim())} />
              {id !== '' && !idOk && <span className="text-label text-fg-muted">Use lower-case letters, digits, dots, dashes or underscores.</span>}
            </span>
          </label>
          <label className={row}>
            <span className={cap}>Name</span>
            <Input aria-label="Name" value={name} maxLength={200} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className={row}>
            <span className={cap}>Summary</span>
            <Input aria-label="Summary" value={summary} maxLength={200} onChange={(e) => setSummary(e.target.value)} />
          </label>
          {kind === 'prompts' && (
            <label className={row}>
              <span className={cap}>Body</span>
              <Textarea aria-label="Body" value={body} rows={8} className="font-mono text-control" onChange={(e) => setBody(e.target.value)} />
            </label>
          )}
          {kind === 'policies' && (
            <p className="text-label text-fg-muted">{carried.length ? `Copies these settings from ${duplicateOf}: ${carried.join(', ')}.` : 'A new policy starts without settings. Open it and choose Edit to set the permission mode, network, sandbox, scopes and rules.'}</p>
          )}
        </div>
        {error && (
          <p role="alert" className="text-xs text-fg-secondary">
            {error}
          </p>
        )}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end [&>button]:w-full sm:[&>button]:w-auto">
          <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="button" size="sm" disabled={busy || !idOk || name.trim() === ''} onClick={submit}>
            {busy ? 'Checking' : 'Review changes'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
