import { type ReactNode, useCallback, useEffect, useState } from 'react'
import Markdown from 'react-markdown'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Segmented } from '../components/segmented'
import { BuilderShell } from './BuilderShell'
import { type Change, type LibraryItem, type LibraryKind, builderApi, errorMessage } from './api'
import { type Draft, PART_FIELDS, type PartKind, buildPartOps, changedPartPaths, changingText, confirmDiscard, newRequestId, partRead } from './changes'
import { ReviewDialog } from './ReviewDialog'
import { useLoad } from './useLoad'
import { LIBRARY_KINDS, NOT_REPORTED } from './vocab'

const SKIP = new Set(['kind', 'id', 'name', 'summary', 'used_by_count', 'editable', 'body'])
const label = (k: string) => k.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase())
const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

/** Nested values as groups and lists, never raw braces. */
function Value({ v }: { v: unknown }): ReactNode {
  if (v === null || v === undefined) return NOT_REPORTED
  if (typeof v === 'boolean') return v ? 'Yes' : 'No'
  if (Array.isArray(v)) {
    if (v.length === 0) return 'none'
    if (v.every((x) => typeof x !== 'object' || x === null)) return v.map(String).join(', ')
    return (
      <ul className="space-y-1">
        {v.map((x, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: static read-only list
          <li key={i}>
            <Value v={x} />
          </li>
        ))}
      </ul>
    )
  }
  if (isObj(v))
    return (
      <dl className="space-y-0.5 border-l border-border-subtle pl-3">
        {Object.entries(v).map(([k, x]) => (
          <div key={k} className="flex flex-wrap gap-x-2">
            <dt className="dash-support text-fg-muted">{label(k)}</dt>
            <dd className="min-w-0">
              <Value v={x} />
            </dd>
          </div>
        ))}
      </dl>
    )
  return String(v)
}

const INPUT = 'w-full border border-border bg-bg px-2 py-1 dash-body text-fg'
const BTN = 'border border-border px-3 py-1 dash-control text-fg-secondary hover:bg-surface-hover disabled:opacity-50'
const GRID = 'grid gap-1 border-b border-border-subtle py-2 md:grid-cols-[10rem_1fr] md:gap-3'

function Detail({ kind, id }: { kind: LibraryKind; id: string }) {
  const [load, retry] = useLoad(() => builderApi.libraryItem(kind, id), [kind, id])
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Draft>({})
  const [review, setReview] = useState<Change | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [raw, setRaw] = useState(false)
  // biome-ignore lint/correctness/useExhaustiveDependencies: a different item starts clean
  useEffect(() => {
    setEditing(false)
    setDraft({})
    setReview(null)
    setError('')
  }, [kind, id])
  const applied = useCallback(() => {
    setEditing(false)
    setDraft({})
    retry()
  }, [retry])
  if (load.status === 'loading') return <div aria-busy="true" className="m-4 h-40 animate-pulse bg-surface" />
  if (load.status === 'error')
    return (
      <div role="alert" className="space-y-2 p-4">
        <p className="dash-body text-fg-secondary">{load.message}</p>
        <button type="button" onClick={retry} className={BTN}>
          Retry
        </button>
      </div>
    )
  const it = load.data
  const partKind: PartKind | null = kind === 'prompts' || kind === 'policies' ? kind : null
  const canEdit = partKind !== null && it.editable !== false
  const fields = partKind ? PART_FIELDS[partKind] : []
  const dirty = partKind ? changedPartPaths(partKind, it, draft) : []
  const body = typeof it.body === 'string' ? it.body : null
  const openReview = async () => {
    if (!partKind) return
    setBusy(true)
    setError('')
    try {
      setReview(await builderApi.createChange({ request_id: newRequestId(), ops: buildPartOps(partKind, it, draft) }))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const SKIP_NOW = partKind === 'prompts' ? SKIP : new Set([...SKIP, 'revision'])
  return (
    <div className="space-y-3 p-5">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="min-w-0 flex-1 text-lg font-semibold text-fg">{it.name}</h2>
        {canEdit && !editing && (
          <button type="button" className={BTN} onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      {!canEdit && (kind === 'skills' || kind === 'tool_servers') && <p className="dash-support text-fg-muted">Read-only: managed in files.</p>}
      {editing && (
        <div role="status" className="flex flex-wrap items-center gap-3 border border-border bg-surface px-3 py-2 dash-support text-fg-secondary">
          <span className="mr-auto">
            {changingText(dirty.map((d) => fields.find((f) => f.path === d)?.label))}
          </span>
          {error && (
            <span role="alert" className="text-danger">
              {error}
            </span>
          )}
          <button
            type="button"
            className={BTN}
            onClick={() => {
              if (!confirmDiscard(dirty.length)) return
              setEditing(false)
              setDraft({})
              setError('')
            }}
          >
            Discard
          </button>
          <button
            type="button"
            disabled={dirty.length === 0 || busy}
            className="border border-fg px-3 py-1 dash-control font-medium text-fg hover:bg-surface-hover disabled:opacity-50"
            onClick={openReview}
          >
            {busy ? 'Checking' : 'Review changes'}
          </button>
        </div>
      )}
      {editing && partKind ? (
        <div>
          {fields.map((f) => {
            const value = f.path in draft ? draft[f.path] : partRead(it, f)
            const set = (v: unknown) => setDraft((d) => ({ ...d, [f.path]: v }))
            return (
              <div key={f.path} className={GRID}>
                <label htmlFor={`lib-${f.path}`} className="dash-support text-fg-muted">
                  {f.label}
                </label>
                {f.kind === 'longtext' ? (
                  <textarea id={`lib-${f.path}`} className={`${INPUT} font-mono`} rows={14} value={String(value)} onChange={(e) => set(e.target.value)} />
                ) : f.kind === 'chips' ? (
                  <input
                    id={`lib-${f.path}`}
                    className={INPUT}
                    placeholder="comma separated"
                    value={(value as string[]).join(', ')}
                    onChange={(e) =>
                      set(
                        e.target.value
                          .split(',')
                          .map((x) => x.trim())
                          .filter(Boolean),
                      )
                    }
                  />
                ) : (
                  <input id={`lib-${f.path}`} className={INPUT} value={String(value)} onChange={(e) => set(e.target.value)} />
                )}
              </div>
            )
          })}
        </div>
      ) : (
        <>
          {it.summary && <p className="dash-body text-fg-secondary">{it.summary}</p>}
          {body !== null && (
            <section aria-label="Body" className="space-y-2">
              <button type="button" className={BTN} aria-pressed={raw} onClick={() => setRaw((r) => !r)}>
                {raw ? 'Rendered' : 'Raw'}
              </button>
              {raw ? (
                <pre className="max-h-[32rem] overflow-auto bg-surface p-3 font-mono dash-support text-fg">{body}</pre>
              ) : (
                <div className="prose-sm max-w-none dash-body text-fg">
                  <Markdown>{body}</Markdown>
                </div>
              )}
            </section>
          )}
          <dl>
            <div className={GRID}>
              <dt className="dash-support text-fg-muted">Used by</dt>
              <dd className="dash-body text-fg">{it.used_by_count ?? NOT_REPORTED}</dd>
            </div>
            {Object.entries(it)
              .filter(([k]) => !SKIP_NOW.has(k))
              .map(([k, v]) => (
                <div key={k} className={GRID}>
                  <dt className="dash-support text-fg-muted">{label(k)}</dt>
                  <dd className="min-w-0 dash-body text-fg [overflow-wrap:anywhere]">
                    <Value v={v} />
                  </dd>
                </div>
              ))}
          </dl>
        </>
      )}
      {review && (
        <ReviewDialog
          initial={review}
          onClose={() => setReview(null)}
          onApplied={applied}
          onReload={() => {
            setReview(null)
            setDraft({})
            retry()
          }}
        />
      )}
    </div>
  )
}

/** Library of prompts, skills, tool servers and policies. Read-only for now. */
export function BuilderLibraryPage() {
  const { kind: rawKind = 'prompts', id } = useParams()
  const navigate = useNavigate()
  const kind = (LIBRARY_KINDS.find((k) => k.id === rawKind)?.id ?? 'prompts') as LibraryKind
  const [q, setQ] = useState('')
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q), 250)
    return () => clearTimeout(t)
  }, [q])
  const [load, retry] = useLoad(() => builderApi.library(kind, debounced), [kind, debounced])
  const items: LibraryItem[] = load.status === 'ready' ? load.data.items : []
  return (
    <div className="flex h-full min-w-0 flex-col">
      <BuilderShell tab="library" />
      <Segmented<LibraryKind> label="Library kinds" value={kind} onChange={(k) => navigate(`/builder/library/${k}`)} items={LIBRARY_KINDS.map((k) => ({ value: k.id, label: k.label }))} />
      <div className="flex min-h-0 flex-1">
        <div className={`${id ? 'hidden md:flex' : 'flex'} w-full shrink-0 flex-col border-r border-border md:w-80`}>
          <div className="border-b border-border p-3">
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Search library"
              aria-label="Search library"
              className="w-full border border-border bg-bg px-2 py-1 dash-body text-fg"
            />
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {load.status === 'loading' && (
              <div aria-busy="true" className="space-y-2 p-4">
                {[0, 1, 2].map((i) => (
                  <div key={i} className="h-10 animate-pulse bg-surface" />
                ))}
              </div>
            )}
            {load.status === 'error' && (
              <div role="alert" className="space-y-2 p-4">
                <p className="dash-body text-fg-secondary">{load.message}</p>
                <button type="button" onClick={retry} className="border border-border px-2 py-1 dash-control text-fg-secondary hover:bg-surface-hover">
                  Retry
                </button>
              </div>
            )}
            {load.status === 'ready' && items.length === 0 && <p className="p-4 dash-body text-fg-faint">Nothing here.</p>}
            <ul aria-label="Library items" className="divide-y divide-border-subtle">
              {items.map((it) => (
                <li key={it.id}>
                  <button
                    type="button"
                    aria-current={it.id === id ? 'true' : undefined}
                    onClick={() => navigate(`/builder/library/${kind}/${encodeURIComponent(it.id)}`)}
                    className={`block w-full px-4 py-2 text-left hover:bg-surface-hover ${it.id === id ? 'bg-surface' : ''}`}
                  >
                    <span className="block truncate dash-body text-fg">{it.name}</span>
                    <span className="block truncate dash-support text-fg-muted">{it.summary || (it.used_by_count != null ? `Used by ${it.used_by_count}` : '')}</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        </div>
        <div className={`${id ? 'flex' : 'hidden md:flex'} min-w-0 flex-1 flex-col overflow-auto`}>
          {id ? (
            <>
              <Link to={`/builder/library/${kind}`} className="px-5 pt-3 dash-control text-fg-muted hover:text-fg md:hidden">
                ← {LIBRARY_KINDS.find((k) => k.id === kind)?.label}
              </Link>
              <Detail kind={kind} id={decodeURIComponent(id)} />
            </>
          ) : (
            <p className="p-5 dash-body text-fg-faint">Select an item to read it.</p>
          )}
        </div>
      </div>
    </div>
  )
}
