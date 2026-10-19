import { createPlannerId } from '@planner/api/identity'
import { isUUID } from '@planner/api/client'
import { RecordTag } from '@hollis-labs/kit-dashboard/data'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { HostLinkText } from '@tachyon/host-sdk'
import { Link } from 'react-router-dom'
import { Check, ExternalLink, Pencil, Plus, Trash2 } from 'lucide-react'
import { Button, CopyableId, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Skeleton, Textarea } from '@hollis-labs/design-components'
import { PlannerFields } from '@planner/components/planner/planner-fields'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import { PlannerFact } from '@planner/components/planner/planner-fact'
import { TorqueTaskRef } from '@planner/components/planner/torque-task-ref'
import { WorkstreamTasksPanel } from '@planner/components/planner/workstream-tasks-panel'
import { usePlannerApi as useApi } from '@planner/api/context'
import { usePlannerSchema } from '@planner/hooks/use-planner-schema'
import { usePlannerTaskTitles } from '@planner/hooks/use-planner-task-titles'
import { PlannerApiError as ApiError, isConflict } from '@planner/api/errors'
import { PlannerWriteError } from './planner-write-error'
import {
  PLANNER_DBS,
  buildPatch,
  dbOfId,
  draftFrom,
  fieldSpecs,
  itemFacts,
  linkTarget,
  plannerMeta,
  validateLinkDraft,
  validationErrors,
  type Draft,
  type FieldError,
} from '@planner/lib/planner'
import { notifyError, notifySuccess } from '@planner/api/notifications'
import type { PlannerDb, PlannerItem, PlannerLink, PlannerTaskBrief } from '@planner/api/types'

interface PlannerDetailDialogProps {
  db: PlannerDb
  id: string
  /** Bumped by the page when the server says something changed. */
  refreshKey?: unknown
  onClose: () => void
  /** The item changed: the page reloads its list. */
  onChanged: () => void
  /** Open another planner item (a reference chip, a promoted item). */
  onOpen: (db: PlannerDb, id: string) => void
}

const REFERENCE_FIELDS = (name: string) => name === 'related_ids' || name === 'depends_on' || name.endsWith('_ids')

function when(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function PlannerDetailDialog({ db, id, refreshKey, onClose, onChanged, onOpen }: PlannerDetailDialogProps) {
  const api = useApi()
  const schema = usePlannerSchema(db)
  const meta = plannerMeta(db)
  const [item, setItem] = useState<PlannerItem | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Draft>({})
  const [fieldErrors, setFieldErrors] = useState<FieldError[]>([])
  const [busy, setBusy] = useState(false)
  const [writeError, setWriteError] = useState<unknown>(null)
  const [editBase, setEditBase] = useState<PlannerItem | null>(null)
  const [newerAvailable, setNewerAvailable] = useState(false)
  const readVersion = useRef(0)

  const specs = useMemo(() => (schema ? fieldSpecs(schema) : []), [schema])
  // Every Torque task the item points at, through a torque link or a `tasks` list: one lookup for the lot.
  const taskIds = useMemo(() => torqueIdsOf(item), [item])
  const titles = usePlannerTaskTitles(taskIds)

  const load = useCallback(async (refresh = false) => {
    const version = ++readVersion.current
    try {
      const next = await api.plannerGet(db, id)
      if (version !== readVersion.current) return
      setItem(previous => {
        if (refresh && previous) {
          if (previous.rev !== next.rev) setNewerAvailable(true)
          return previous // never replace the revision the operator is reviewing
        }
        return next
      })
      setError(null)
      return true
    } catch (err) {
      if (version === readVersion.current) setError(err instanceof Error ? err.message : 'Failed to load')
      return false
    }
  }, [api, db, id])

  useEffect(() => {
    setItem(null)
    setEditing(false)
    void load()
    return () => { readVersion.current++ }
  }, [load])
  useEffect(() => { if (refreshKey !== undefined) void load(true) }, [refreshKey, load])

  async function reloadLatest() {
    if (!(await load())) return
    setNewerAvailable(false)
    setWriteError(null)
    // Keep the draft and its original base: only fields the operator changed
    // are included in the next patch, after explicit review of the new revision.
  }

  /** Run a write; put its result on screen and tell the page. */
  async function write<T extends PlannerItem>(fn: () => Promise<T>, failure: string): Promise<T | null> {
    if (busy || isConflict(writeError)) return null
    setBusy(true)
    setWriteError(null)
    readVersion.current++
    try {
      const next = await fn()
      setItem(next)
      onChanged()
      return next
    } catch (err) {
      setWriteError(err)
      notifyError(err, failure)
      return null
    } finally {
      setBusy(false)
    }
  }

  function startEdit() {
    if (!item) return
    setEditBase(item)
    setDraft(draftFrom(specs, item))
    setFieldErrors([])
    setEditing(true)
  }

  async function saveEdit() {
    if (!item || busy || isConflict(writeError)) return
    const patch = buildPatch(specs, editBase ?? item, draft)
    if (Object.keys(patch).length === 0) {
      setEditing(false)
      return
    }
    setBusy(true)
    setFieldErrors([])
    setWriteError(null)
    readVersion.current++
    try {
      const next = await api.plannerUpdate(db, item.id, patch, item.rev)
      setItem(next)
      setEditing(false)
      onChanged()
    } catch (err) {
      if (err instanceof ApiError && err.code === 'invalid') {
        setFieldErrors(validationErrors(err.details))
        notifyError(err, 'Some fields are not valid')
      } else {
        setWriteError(err)
        if (!isConflict(err)) notifyError(err, 'Failed to save')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="dash-detail-dialog max-h-[88vh] overflow-auto" widthClassName="max-w-3xl" aria-describedby={undefined}>
        {error && !item ? (
          <>
            <DialogHeader>
              <DialogTitle>{id}</DialogTitle>
              <DialogDescription>{error}</DialogDescription>
            </DialogHeader>
          </>
        ) : !item ? (
          <>
            <DialogHeader>
              <DialogTitle>{id}</DialogTitle>
            </DialogHeader>
            <Skeleton className="h-24 w-full" />
          </>
        ) : (
          <div className="flex flex-col gap-4">
            <DialogHeader>
              <div className="flex flex-wrap items-center gap-2 pr-8">
                <CopyableId id={item.id} />
                <PlannerStatus status={item.status} />
                {itemFacts(db, item).map((f) => <PlannerFact key={f} db={db} fact={f} />)}
              </div>
              <DialogTitle className="dash-body leading-6">{item.title}</DialogTitle>
              {item.tldr && <DialogDescription className="whitespace-pre-wrap">{item.tldr}</DialogDescription>}
            </DialogHeader>

            {error && <p role="alert" className="dash-body text-destructive">{error}</p>}
            <PlannerWriteError error={writeError} onReload={() => void reloadLatest()} />
            {newerAvailable && <div role="status" className="flex items-center gap-2 dash-body text-fg-muted">A newer version is available. Your draft is preserved.<Button size="sm" variant="outline" onClick={() => void reloadLatest()}>Reload latest version</Button></div>}
            <div className="flex flex-wrap items-center gap-2">
              {!editing && (
                <Button size="sm" variant="outline" className="gap-1" onClick={startEdit} disabled={!schema || busy}>
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </Button>
              )}
              <span className="ml-auto dash-label leading-4 text-fg-muted">
                rev {item.rev}
                {item.updated ? ` · updated ${item.updated}` : ''}
                {item.author ? ` · ${item.author}` : ''}
              </span>
            </div>

            {db === 'decisions' && <DecisionPanel item={item} busy={busy || isConflict(writeError)} write={write} />}
            {db === 'inbox' && <InboxPanel item={item} busy={busy || isConflict(writeError)} write={write} onOpen={onOpen} onChanged={onChanged} onReload={() => void reloadLatest()} />}

            {editing ? (
              <div className="flex flex-col gap-3 rounded-md border border-border p-4">
                <PlannerFields specs={specs} draft={draft} errors={fieldErrors} idPrefix="pe" onChange={(name, value) => setDraft((d) => ({ ...d, [name]: value }))} />
                <div className="flex justify-end gap-2">
                  <Button variant="outline" size="sm" onClick={() => setEditing(false)} disabled={busy}>
                    Cancel
                  </Button>
                  <Button size="sm" onClick={saveEdit} disabled={busy || isConflict(writeError)}>
                    {busy ? 'Saving…' : 'Save'}
                  </Button>
                </div>
              </div>
            ) : (
              <FieldList item={item} specs={specs.map((s) => s.name)} schemaLabels={Object.fromEntries(specs.map((s) => [s.name, s.label]))} titles={titles} onOpen={onOpen} />
            )}

            {db === 'workstreams' && <WorkstreamTasksPanel id={item.id} refreshKey={refreshKey} />}

            <LinksPanel
              item={item}
              kinds={schema?.link_kinds ?? []}
              titles={titles}
              busy={busy || isConflict(writeError)}
              onAdd={(link) => write(() => api.plannerLinkAdd(db, item.id, link, item.rev), 'Failed to add link')}
              onRemove={(link) => write(() => api.plannerLinkRemove(db, item.id, link.kind, link.ref, item.rev), 'Failed to remove link')}
            />

            <CommentsPanel item={item} busy={busy || isConflict(writeError)} onAdd={(text) => write(() => api.plannerComment(db, item.id, text, item.rev), 'Failed to add comment')} />
          </div>
        )}
        <span className="sr-only">{meta.label}</span>
      </DialogContent>
    </Dialog>
  )
}

// ---------- fields (read-only view) ----------

const SKIP_IN_LIST = new Set(['title', 'tldr', 'status', 'kind'])

const TORQUE_ID = /^[A-Z]{2,6}-\d{8}-\d{3,6}$/

/** Torque task ids an item points at: its torque links, and the `tasks` list of a roadmap item. */
function torqueIdsOf(item: PlannerItem | null): string[] {
  if (!item) return []
  const ids = new Set<string>()
  for (const l of item.links ?? []) if (l.kind === 'torque' && TORQUE_ID.test(l.ref)) ids.add(l.ref)
  if (Array.isArray(item.tasks)) for (const t of item.tasks) if (typeof t === 'string' && TORQUE_ID.test(t)) ids.add(t)
  return [...ids]
}

type Titles = Record<string, PlannerTaskBrief | null>

function FieldList({ item, specs, schemaLabels, titles, onOpen }: { item: PlannerItem; specs: string[]; schemaLabels: Record<string, string>; titles: Titles; onOpen: (db: PlannerDb, id: string) => void }) {
  const names = [...new Set([...specs, ...Object.keys(item)])].filter(n => !SKIP_IN_LIST.has(n) && !['id', 'rev', 'created', 'updated', 'author', 'comments', 'links', 'options', 'references'].includes(n))
  const rows = names.filter((n) => {
    const v = item[n]
    return v !== undefined && v !== null && v !== '' && v !== false && !(Array.isArray(v) && v.length === 0)
  })
  if (rows.length === 0) return <p className="dash-body leading-5 text-fg-muted">No other fields set.</p>
  return (
    <dl className="grid grid-cols-1 gap-x-4 gap-y-2 border-y border-border-subtle py-3 sm:grid-cols-[calc(var(--spacing)*36)_minmax(0,1fr)]">
      {rows.map((n) => (
        <div key={n} className="contents">
          <dt className="font-mono dash-caption uppercase tracking-wide text-fg-faint">{schemaLabels[n] ?? n}</dt>
          <dd className="min-w-0 dash-body leading-5">
            <FieldValue name={n} value={item[n]} titles={titles} references={item.references} onOpen={onOpen} />
          </dd>
        </div>
      ))}
    </dl>
  )
}

function FieldValue({ name, value, titles, references, onOpen }: { name: string; value: unknown; titles: Titles; references?: PlannerItem["references"]; onOpen: (db: PlannerDb, id: string) => void }) {
  if (name === 'tasks' && Array.isArray(value)) {
    return (
      <ul className="flex flex-col gap-1">
        {value.map(String).map((id) => (
          <li key={id}>{TORQUE_ID.test(id) ? <TorqueTaskRef id={id} brief={titles[id]} /> : <span className="font-mono dash-label leading-4">{id}</span>}</li>
        ))}
      </ul>
    )
  }
  if (Array.isArray(value) && REFERENCE_FIELDS(name)) {
    return (
      <div className="flex flex-wrap gap-1.5">
        {value.map(String).map((ref) => {
          const resolved = references?.[ref]
          const target = resolved?.db ?? dbOfId(ref)
          return target ? (
            <Button key={ref} variant="outline" size="xs" className="h-auto min-h-6 max-w-full whitespace-normal break-all py-1 text-left font-mono dash-label" onClick={() => onOpen(target, resolved?.id ?? ref)}>
              {ref}
            </Button>
          ) : isUUID(ref) ? (
            <UnresolvedReference key={ref} id={ref} onOpen={onOpen} />
          ) : (
            <RecordTag key={ref}>
              {ref}
            </RecordTag>
          )
        })}
      </div>
    )
  }
  if (Array.isArray(value)) {
    return (
      <ul className="list-disc pl-4">
        {value.map((v, i) => (
          <li key={i}>{typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v)}</li>
        ))}
      </ul>
    )
  }
  if (typeof value === 'object' && value !== null) return <pre className="whitespace-pre-wrap break-words font-mono dash-label">{JSON.stringify(value, null, 2)}</pre>
  if (typeof value === 'boolean') return <span>{value ? 'Yes' : 'No'}</span>
  return <span className="whitespace-pre-wrap break-words">{String(value)}</span>
}

function UnresolvedReference({ id, onOpen }: { id: string; onOpen: (db: PlannerDb, id: string) => void }) {
  const api = useApi()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function open() {
    setBusy(true)
    setError(null)
    try {
      const target = await api.resolveReference(null, id)
      onOpen(target.db, target.id)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to resolve this reference.')
    } finally { setBusy(false) }
  }
  return <span className="min-w-0">
    <Button variant="outline" size="xs" className="h-auto min-h-6 max-w-full whitespace-normal break-all py-1 text-left font-mono dash-label" disabled={busy} onClick={() => void open()}>{id}</Button>
    {error && <span role="alert" className="block dash-label text-warning">{error}</span>}
  </span>
}

// ---------- links ----------

function LinksPanel({ item, kinds, titles, busy, onAdd, onRemove }: { item: PlannerItem; kinds: string[]; titles: Titles; busy: boolean; onAdd: (l: PlannerLink) => Promise<unknown>; onRemove: (l: PlannerLink) => Promise<unknown> }) {
  const [adding, setAdding] = useState(false)
  const [kind, setKind] = useState('torque')
  const [ref, setRef] = useState('')
  const [label, setLabel] = useState('')
  const problem = ref ? validateLinkDraft(kind, ref) : null
  const links = item.links ?? []

  async function add() {
    const done = await onAdd({ kind, ref: ref.trim(), ...(label.trim() ? { label: label.trim() } : {}) })
    if (done) {
      setRef('')
      setLabel('')
      setAdding(false)
    }
  }

  return (
    <section aria-label="Links" className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <h3 className="dash-body leading-5 font-semibold">Links</h3>
        {!adding && (
          <Button size="sm" variant="ghost" className="gap-1" onClick={() => setAdding(true)} disabled={busy || kinds.length === 0}>
            <Plus className="h-3.5 w-3.5" /> Add link
          </Button>
        )}
      </div>
      {links.length === 0 && !adding && <p className="dash-body leading-5 text-fg-muted">Nothing linked. Point this at Torque tasks, PRs, notes or files.</p>}
      <ul className="flex flex-col gap-1">
        {links.map((l) => {
          const target = linkTarget(l)
          const text = l.label ? `${l.label} (${l.ref})` : l.ref
          return (
            <li key={`${l.kind}:${l.ref}`} className="flex items-center gap-2 dash-body leading-5">
              <span className="w-20 shrink-0 dash-label leading-4 uppercase tracking-wide text-fg-muted">{l.kind}</span>
              {l.kind === 'torque' && target.to ? (
                <TorqueTaskRef id={l.ref} brief={titles[l.ref]} label={l.label} />
              ) : target.to ? (
                <Link to={target.to} className="min-w-0 truncate font-mono text-primary hover:underline">
                  {text}
                </Link>
              ) : target.href ? (
                <a href={target.href} target="_blank" rel="noreferrer noopener" className="inline-flex min-w-0 items-center gap-1 truncate text-primary hover:underline">
                  {text} <ExternalLink className="h-3 w-3 shrink-0" />
                </a>
              ) : (
                <span className="min-w-0 truncate font-mono">{text}</span>
              )}
              <Button size="sm" variant="ghost" className="ml-auto h-6 w-6 p-0" aria-label={`Remove ${l.kind} link ${l.ref}`} onClick={() => onRemove(l)} disabled={busy}>
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </li>
          )
        })}
      </ul>
      {adding && (
        <div className="flex flex-col gap-2 rounded-md border border-border p-3">
          <div className="grid gap-2 sm:grid-cols-[9rem_1fr_1fr]">
            <Select value={kind} onValueChange={(v) => v && setKind(v)}>
              <SelectTrigger className="h-8 dash-body leading-5" aria-label="Link kind">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {kinds.map((k) => (
                  <SelectItem key={k} value={k}>
                    {k}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input value={ref} onChange={(e) => setRef(e.target.value)} placeholder="Task id, URL, path…" aria-label="Link target" />
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Label (optional)" aria-label="Link label" />
          </div>
          {problem && <p className="dash-label leading-4 text-destructive">{problem}</p>}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="outline" onClick={() => setAdding(false)}>
              Cancel
            </Button>
            <Button size="sm" onClick={add} disabled={busy || !ref.trim() || !!problem}>
              Add link
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}

// ---------- comments ----------

function CommentsPanel({ item, busy, onAdd }: { item: PlannerItem; busy: boolean; onAdd: (text: string) => Promise<unknown> }) {
  const [text, setText] = useState('')
  const comments = item.comments ?? []

  async function add() {
    const t = text.trim()
    if (!t) return
    if (await onAdd(t)) setText('')
  }

  return (
    <section aria-label="Comments" className="flex flex-col gap-2">
      <h3 className="dash-body leading-5 font-semibold">Comments{comments.length ? ` (${comments.length})` : ''}</h3>
      <ul className="flex flex-col gap-3">
        {comments.map((c) => (
          <li key={c.id} className={`rounded-md border p-3 dash-body leading-5 ${c.kind === 'decision' ? 'border-primary/40 bg-primary/5' : 'border-border'}`}>
            <div className="mb-1 flex items-center gap-2 dash-label leading-4 text-fg-muted">
              <span className="font-medium text-foreground">{c.author || 'unknown'}</span>
              <span>{when(c.created)}</span>
              {c.kind === 'decision' && <span className="rounded bg-primary/10 px-1.5 py-0.5 text-primary">decision</span>}
            </div>
            <p className="whitespace-pre-wrap break-words"><HostLinkText surface="planner-comments" text={c.text} /></p>
          </li>
        ))}
      </ul>
      <Label htmlFor="planner-new-comment" className="sr-only">
        New comment
      </Label>
      <Textarea
        id="planner-new-comment"
        value={text}
        rows={2}
        placeholder="Add a comment…"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault()
            void add()
          }
        }}
      />
      <div className="flex justify-end">
        <Button size="sm" onClick={add} disabled={busy || !text.trim()}>
          Comment
        </Button>
      </div>
    </section>
  )
}

// ---------- decisions ----------

type Write = <T extends PlannerItem>(fn: () => Promise<T>, failure: string) => Promise<T | null>

function DecisionPanel({ item, busy, write }: { item: PlannerItem; busy: boolean; write: Write }) {
  const api = useApi()
  const [comment, setComment] = useState('')
  const options = item.options ?? []
  const deferred = item.status === 'deferred' || item.deferred === true
  const open = item.status === 'needs-decision' || item.status === 'proposed' || deferred
  const chosen = typeof item.selected_option === 'string' ? item.selected_option : ''

  return (
    <section aria-label="Decision" className="flex flex-col gap-3 rounded-md border border-border p-4">
      <div className="flex items-center justify-between">
        <h3 className="dash-body leading-5 font-semibold">Decision</h3>
        {(!open || deferred) && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => write(() => api.plannerReopen(item.id, item.rev), 'Failed to reopen')}>
            Reopen
          </Button>
        )}
      </div>
      {deferred && typeof item.defer_note === 'string' && item.defer_note && (
        <p className="dash-body leading-5 text-fg-muted">
          <span className="font-medium text-foreground">Deferred: </span>
          {item.defer_note}
        </p>
      )}
      {typeof item.decision === 'string' && item.decision && <p className="whitespace-pre-wrap dash-body leading-5">{item.decision}</p>}
      {options.length > 0 && (
        <ul className="flex flex-col gap-2">
          {options.map((o) => (
            <li key={o.id} className={`rounded-md border p-3 dash-body leading-5 ${o.id === chosen ? 'border-primary bg-primary/5' : 'border-border'}`}>
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono dash-label leading-4 text-fg-muted">{o.id}</span>
                <span className="font-medium">{o.label}</span>
                {o.recommended && <span className="rounded bg-muted px-1.5 py-0.5 dash-label">recommended</span>}
                {o.id === chosen && (
                  <span className="inline-flex items-center gap-1 dash-label leading-4 text-primary">
                    <Check className="h-3 w-3" /> chosen
                  </span>
                )}
                {open && (
                  <Button
                    size="sm"
                    className="ml-auto"
                    disabled={busy}
                    aria-label={`Choose option ${o.id}: ${o.label}`}
                    onClick={() => write(() => api.plannerDecide(item.id, o.id, item.rev, comment.trim() || undefined), 'Failed to record the decision')}
                  >
                    Choose
                  </Button>
                )}
              </div>
              {o.summary && <p className="mt-1 text-fg-muted"><HostLinkText surface="planner-comments" text={o.summary} /></p>}
              {(o.pros || o.cons) && (
                <div className="mt-2 grid gap-2 sm:grid-cols-2">
                  {o.pros && <p><span className="font-medium">Pros: </span>{o.pros}</p>}
                  {o.cons && <p><span className="font-medium">Cons: </span>{o.cons}</p>}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {options.length === 0 && open && <p className="dash-body leading-5 text-fg-muted">This decision has no options to choose from yet.</p>}
      {open && (
        <div className="flex flex-col gap-2">
          <Textarea value={comment} rows={2} placeholder="Why (recorded with the decision)…" aria-label="Decision comment" onChange={(e) => setComment(e.target.value)} />
          {open && !deferred && (
            <div>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => write(() => api.plannerDefer(item.id, item.rev, comment.trim() || undefined), 'Failed to defer')}>
                Defer
              </Button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

// ---------- inbox ----------

function InboxPanel({ item, busy, write, onOpen, onChanged, onReload }: { item: PlannerItem; busy: boolean; write: Write; onOpen: (db: PlannerDb, id: string) => void; onChanged: () => void; onReload: () => void }) {
  const api = useApi()
  const [to, setTo] = useState<PlannerDb>('ideas')
  const [note, setNote] = useState('')
  const [promoting, setPromoting] = useState(false)
  const [promotionError, setPromotionError] = useState<unknown>(null)
  const targetIds = useRef<Partial<Record<PlannerDb, string>>>({})
  useEffect(() => { setPromotionError(null) }, [item.rev])
  const triageable = item.status === 'new' || item.status === 'triaged'
  const promoted = item.promoted_to as { db?: string; id?: string } | undefined

  if (promoted?.id || !triageable) {
    return promoted?.id && promoted.db ? (
      <p className="dash-body leading-5">
        Promoted to{' '}
        <Button variant="link" size="xs" className="font-mono dash-label" onClick={() => onOpen(promoted.db as PlannerDb, promoted.id as string)}>
          {promoted.id}
        </Button>
      </p>
    ) : null
  }

  async function promote() {
    setPromoting(true)
    try {
      const targetId = targetIds.current[to] ??= createPlannerId()
      const res = await api.plannerInboxPromote(item.id, to, item.rev, targetId)
      notifySuccess(`Promoted to ${res.item.id}`)
      onChanged()
      onOpen(to, res.item.id)
    } catch (err) {
      setPromotionError(err)
      notifyError(err, 'Failed to promote')
    } finally {
      setPromoting(false)
    }
  }

  return (
    <section aria-label="Triage" className="flex flex-col gap-3 rounded-md border border-border p-4">
      <h3 className="dash-body leading-5 font-semibold">Triage</h3>
      <PlannerWriteError error={promotionError} onReload={onReload} />
      <div className="flex flex-wrap items-center gap-2">
        <span className="dash-body leading-5">Promote to</span>
        <Select value={to} onValueChange={(v) => v && setTo(v as PlannerDb)}>
          <SelectTrigger className="h-8 w-40 dash-body leading-5" aria-label="Promote to">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {PLANNER_DBS.filter((d) => d.key !== 'inbox').map((d) => (
              <SelectItem key={d.key} value={d.key}>
                {d.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button size="sm" onClick={promote} disabled={busy || promoting || isConflict(promotionError)}>
          Promote
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input className="max-w-sm" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Why dismiss (optional)" aria-label="Dismiss note" />
        <Button size="sm" variant="outline" disabled={busy || promoting || isConflict(promotionError)} onClick={() => write(() => api.plannerInboxDismiss(item.id, item.rev, note.trim() || undefined), 'Failed to dismiss')}>
          Dismiss
        </Button>
      </div>
    </section>
  )
}
