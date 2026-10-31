import { createPlannerId } from '@planner/api/identity'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@hollis-labs/design-components'
import { PlannerWriteError } from './planner-write-error'
import { PlannerFields } from '@planner/components/planner/planner-fields'
import { usePlannerApi as useApi } from '@planner/api/context'
import { usePlannerSchema } from '@planner/hooks/use-planner-schema'
import { PlannerApiError as ApiError } from '@planner/api/errors'
import { buildNewItem, createDraft, createSpecs, missingRequired, plannerMeta, validationErrors, type Draft, type FieldError } from '@planner/lib/planner'
import { authorizing, notifyError, notifySuccess } from '@planner/api/notifications'
import type { PlannerDb, PlannerItem } from '@planner/api/types'

interface PlannerCreateDialogProps {
  db: PlannerDb
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The rank a new priority starts at. */
  nextRank?: number
  /** Prefills the title, e.g. what was typed in the palette. */
  initialTitle?: string
  onCreated: (item: PlannerItem) => void
}

export function PlannerCreateDialog({ db, open, onOpenChange, nextRank = 1, initialTitle, onCreated }: PlannerCreateDialogProps) {
  const api = useApi()
  const schema = usePlannerSchema(db)
  const meta = plannerMeta(db)
  const specs = useMemo(() => (schema ? createSpecs(schema) : []), [schema])
  const [draft, setDraft] = useState<Draft>({})
  const [errors, setErrors] = useState<FieldError[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [writeError, setWriteError] = useState<unknown>(null)
  const retainedId = useRef('')

  useEffect(() => {
    if (open && schema) {
      setDraft({ ...createDraft(db, specs, nextRank), ...(initialTitle ? { title: initialTitle } : {}) })
      setErrors([])
      setWriteError(null)
      retainedId.current = createPlannerId()
    }
    // Reseed when the dialog opens or the schema arrives, not on every keystroke.
  }, [open, schema, db])

  const missing = missingRequired(specs, draft)

  async function submit() {
    if (missing.length > 0) return
    setSubmitting(true)
    setErrors([])
    try {
      const created = await authorizing(() => api.plannerCreate(db, buildNewItem(specs, draft), retainedId.current))
      notifySuccess(`Created ${created.id}`)
      onOpenChange(false)
      onCreated(created)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'invalid') setErrors(validationErrors(err.details))
      setWriteError(err)
      notifyError(err, `Failed to create ${meta.singular}`)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="dash-detail-dialog max-h-[85vh] overflow-auto" widthClassName="max-w-lg">
        <DialogHeader>
          <DialogTitle>New {meta.singular}</DialogTitle>
          <DialogDescription>Fill in the essentials. Everything else can be edited once it exists.</DialogDescription>
        </DialogHeader>
        <PlannerWriteError error={writeError} />
        {schema ? (
          <PlannerFields specs={specs} draft={draft} errors={errors} idPrefix="pc" onChange={(name, value) => setDraft((d) => ({ ...d, [name]: value }))} />
        ) : (
          <p className="dash-body leading-5 text-fg-muted">Loading…</p>
        )}
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button size="sm" onClick={submit} disabled={!schema || submitting || missing.length > 0} title={missing.length ? `Needs: ${missing.join(', ')}` : undefined}>
            {submitting ? 'Creating…' : `Create ${meta.singular}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
