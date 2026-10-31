import { useState } from 'react'
import { Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogTitle, Input } from '@hollis-labs/design-components'
import { type Change, type ChangeOp, builderApi, errorMessage } from './api'
import { type ReviewRow, newRequestId } from './changes'
import { ReviewDialog } from './ReviewDialog'

export interface EntityField {
  /** Field path for the change op (editor-model path for agents, flat file field for launches). */
  path: string
  label: string
  kind: 'text' | 'bool'
  hint?: string
  required?: boolean
}

export const AGENT_FIELDS: EntityField[] = [
  { path: 'identity.name', label: 'Name', kind: 'text', required: true },
  { path: 'identity.description', label: 'Summary', kind: 'text' },
  { path: 'runtime.provider', label: 'Provider', kind: 'text', hint: 'for example claude' },
  { path: 'runtime.model', label: 'Model', kind: 'text' },
]

export const LAUNCH_FIELDS: EntityField[] = [
  { path: 'agent', label: 'Agent', kind: 'text', required: true, hint: 'agent id' },
  { path: 'provider', label: 'Provider', kind: 'text' },
  { path: 'project', label: 'Project', kind: 'text', hint: 'project id' },
  { path: 'worker', label: 'Worker', kind: 'text' },
  { path: 'workspace.mode', label: 'Workspace mode', kind: 'text' },
  { path: 'route', label: 'Route', kind: 'text' },
  { path: 'chat', label: 'Chat', kind: 'bool' },
  { path: 'enabled', label: 'Enabled', kind: 'bool' },
]

type Values = Record<string, string | boolean>
const SLUG = /^[a-z0-9][a-z0-9._-]{0,63}$/

/**
 * Create, duplicate or edit one catalog entity (agent or launch). Never saves by itself: it builds change ops,
 * asks the backend to compute the change, and hands it to the Review dialog, so the save mode decides what happens.
 */
export function EntityDialog({ kind, mode, fields, initial, id: fixedId, sha, noun, onClose, onApplied }: {
  kind: 'agent' | 'launch'
  mode: 'create' | 'edit'
  fields: EntityField[]
  initial: Values
  id?: string
  sha?: string | null
  noun: string
  onClose: () => void
  onApplied: () => void
}) {
  const [id, setId] = useState(fixedId ?? '')
  const [vals, setVals] = useState<Values>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [review, setReview] = useState<{ change: Change; rows: ReviewRow[] } | null>(null)
  const changed = fields.filter((f) => (vals[f.path] ?? (f.kind === 'bool' ? false : '')) !== (initial[f.path] ?? (f.kind === 'bool' ? false : '')))
  const missing = fields.filter((f) => f.required && !String(vals[f.path] ?? '').trim())
  const idOk = mode === 'edit' || SLUG.test(id)
  const toSend = mode === 'create' ? fields.filter((f) => (f.kind === 'bool' ? vals[f.path] !== undefined : String(vals[f.path] ?? '').trim() !== '')) : changed
  const submit = async () => {
    setBusy(true)
    setError('')
    const target = { kind, id } as ChangeOp['target']
    const ops: ChangeOp[] = [
      ...(mode === 'create' ? [{ target, op: { type: 'create' as const } }] : []),
      ...toSend.map((f) => ({ target, ...(mode === 'edit' ? { expected_sha: sha ?? '' } : {}), op: { type: 'set' as const, path: f.path, value: vals[f.path] } })),
    ]
    const show = (v: unknown) => (typeof v === 'boolean' ? (v ? 'Yes' : 'No') : String(v ?? '') || 'empty')
    const rows: ReviewRow[] = [
      ...(mode === 'create' ? [{ label: 'New', from: 'does not exist', to: id }] : []),
      ...toSend.map((f) => ({ label: f.label, from: show(initial[f.path]), to: show(vals[f.path]) })),
    ]
    try {
      setReview({ change: await builderApi.createChange({ request_id: newRequestId(), ops }), rows })
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
          onApplied()
          onClose()
        }}
        onReload={() => {
          setReview(null)
        }}
      />
    )
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent widthClassName="max-w-xl" className="gap-3 max-sm:top-auto max-sm:bottom-0 max-sm:left-0 max-sm:max-h-[88vh] max-sm:w-full max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:overflow-auto max-sm:rounded-b-none max-sm:rounded-t-lg">
        <DialogTitle className="font-sans text-base font-semibold normal-case tracking-normal">{mode === 'create' ? `New ${noun}` : `Edit ${noun} · ${fixedId}`}</DialogTitle>
        <DialogDescription className="text-xs text-fg-muted">Nothing changes until you review the change and it is applied.</DialogDescription>
        <div className="flex flex-col gap-2">
          {mode === 'create' && (
            <label className="grid gap-1 sm:grid-cols-[130px_1fr] sm:gap-3">
              <span className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-2">Id</span>
              <span className="flex flex-col gap-1">
                <Input aria-label="Id" value={id} placeholder="lower-case-slug" onChange={(e) => setId(e.target.value.trim())} />
                {id !== '' && !idOk && <span className="text-label text-fg-muted">Use lower-case letters, digits, dots, dashes or underscores.</span>}
              </span>
            </label>
          )}
          {fields.map((f) => (
            <label key={f.path} className="grid gap-1 sm:grid-cols-[130px_1fr] sm:gap-3">
              <span className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-2">{f.label}</span>
              {f.kind === 'bool' ? (
                <span className="flex h-8 items-center">
                  <Checkbox aria-label={f.label} checked={vals[f.path] === true} onCheckedChange={(v) => setVals((s) => ({ ...s, [f.path]: v === true }))} />
                </span>
              ) : (
                <Input aria-label={f.label} value={String(vals[f.path] ?? '')} placeholder={f.hint} onChange={(e) => setVals((s) => ({ ...s, [f.path]: e.target.value }))} />
              )}
            </label>
          ))}
        </div>
        {error && (
          <p role="alert" className="text-xs text-fg-secondary">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="button" size="sm" disabled={busy || !idOk || (mode === 'create' && id === '') || missing.length > 0 || (mode === 'edit' && changed.length === 0)} onClick={submit}>
            {busy ? 'Checking' : 'Review changes'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
