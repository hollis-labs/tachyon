import { useState } from 'react'
import { Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogTitle, Input } from '@hollis-labs/design-components'
import { type Change, type ChangeOp, builderApi, errorMessage } from './api'
import { type ReviewRow, newRequestId } from './changes'
import { ReviewDialog } from './ReviewDialog'

const KEY = /^[A-Z_][A-Z0-9_]{0,127}$/
const MAX_VALUE = 4096

/**
 * Set one environment variable on a launch. Values are write-only: the field is a password input, the value goes to the
 * backend inside the change and is never shown in the review rows or kept after the dialog closes.
 */
export function EnvDialog({ launchId, sha, onClose, onApplied }: { launchId: string; sha?: string | null; onClose: () => void; onApplied: () => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const [remove, setRemove] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [review, setReview] = useState<{ change: Change; rows: ReviewRow[] } | null>(null)
  const ok = KEY.test(key) && (remove || (value !== '' && new TextEncoder().encode(value).length <= MAX_VALUE))
  const submit = async () => {
    setBusy(true)
    setError('')
    const ops: ChangeOp[] = [{ target: { kind: 'launch', id: launchId }, expected_sha: sha ?? '', op: remove ? { type: 'remove', path: `env.${key}` } : { type: 'set', path: `env.${key}`, value } }]
    try {
      const change = await builderApi.createChange({ request_id: newRequestId(), ops })
      setValue('')
      setReview({ change, rows: [{ label: key, from: 'current value is never shown', to: remove ? 'removed' : 'new value (hidden)' }] })
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  if (review)
    return <ReviewDialog initial={review.change} subject={launchId} rows={review.rows} onClose={() => setReview(null)} onApplied={() => { onApplied(); onClose() }} onReload={() => setReview(null)} />
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent widthClassName="max-w-md" className="gap-3 max-sm:top-auto max-sm:bottom-0 max-sm:left-0 max-sm:max-h-[88vh] max-sm:w-full max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:overflow-auto max-sm:rounded-b-none max-sm:rounded-t-lg">
        <DialogTitle className="font-sans text-base font-semibold normal-case tracking-normal">Set variable · {launchId}</DialogTitle>
        <DialogDescription className="text-xs text-fg-muted">Values are write-only. A running session keeps the value it started with.</DialogDescription>
        <div className="flex flex-col gap-2">
          <label className="grid gap-1 sm:grid-cols-[100px_1fr] sm:gap-3">
            <span className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-2">Name</span>
            <Input aria-label="Variable name" value={key} placeholder="API_BASE" autoComplete="off" onChange={(e) => setKey(e.target.value.trim().toUpperCase())} />
          </label>
          <label className="grid gap-1 sm:grid-cols-[100px_1fr] sm:gap-3">
            <span className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-2">Value</span>
            <Input aria-label="Variable value" type="password" disabled={remove} value={value} autoComplete="off" spellCheck={false} onChange={(e) => setValue(e.target.value)} />
          </label>
          <label className="flex items-center gap-2 text-xs text-fg-secondary sm:pl-[112px]">
            <Checkbox aria-label="Remove this variable" checked={remove} onCheckedChange={(v) => setRemove(v === true)} />
            Remove this variable instead
          </label>
          {key !== '' && !KEY.test(key) && <span className="text-label text-fg-muted">Upper-case letters, digits and underscores; do not start with a digit.</span>}
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
          <Button type="button" size="sm" disabled={busy || !ok} onClick={submit}>
            {busy ? 'Checking' : 'Review changes'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
