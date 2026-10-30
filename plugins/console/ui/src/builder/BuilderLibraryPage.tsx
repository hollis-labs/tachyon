import { useDiscardGuard } from './DiscardGuard'
import { type ReactNode, useCallback, useEffect, useState } from 'react'
import { Button, Input, Textarea } from '@hollis-labs/design-components'
import Markdown from 'react-markdown'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Segmented } from '../components/segmented'
import { BuilderShell } from './BuilderShell'
import { type Change, type LibraryItem, type LibraryKind, builderApi, errorMessage } from './api'
import { type Draft, PART_FIELDS, type PartKind, buildPartOps, changedPartPaths, changingText, newRequestId, partRead } from './changes'
import { useChangeMode } from './capabilities'
import { ModeNote, PreparedLabel } from './ModeNote'
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

const GRID = 'grid gap-1 border-b border-border-subtle py-2 md:grid-cols-[10rem_1fr] md:gap-3'

const POLICY_LEAD = new Set(['permission_mode', 'network', 'default_sandbox', 'mcp_scopes', 'scope_count', 'privileged', 'bundle', 'referencing_agents'])
const WORD = (v: unknown) => (typeof v === 'string' && v ? v : NOT_REPORTED)

/** A policy is a bundle of settings an agent takes on, then the agents it applies to. */
function PolicyLead({ it }: { it: LibraryItem }) {
  const bundle = isObj(it.bundle) ? it.bundle : {}
  const val = (k: string) => (k in bundle ? bundle[k] : it[k])
  const scopes = Array.isArray(val('mcp_scopes')) ? (val('mcp_scopes') as unknown[]).map(String) : null
  const agents = Array.isArray(it.referencing_agents) ? (it.referencing_agents as { id: string }[]) : null
  return (
    <>
      <section aria-label="Settings" className="space-y-1">
        <h3 className="dash-label font-medium text-fg-muted">Settings</h3>
        <dl>
          {(
            [
              ['Permission mode', WORD(val('permission_mode'))],
              ['Network', WORD(val('network'))],
              ['Sandbox', WORD(val('default_sandbox'))],
              ['Scopes', scopes === null ? (typeof it.scope_count === 'number' ? `${it.scope_count} scopes` : NOT_REPORTED) : scopes.length ? scopes.join(', ') : 'none'],
            ] as const
          ).map(([k, v]) => (
            <div key={k} className={GRID}>
              <dt className="dash-support text-fg-muted">{k}</dt>
              <dd className="dash-body text-fg [overflow-wrap:anywhere]">{v}</dd>
            </div>
          ))}
        </dl>
        {it.privileged === true && <p className="dash-support text-fg-secondary">Includes privileged settings. Changing them needs a confirmation in the Review step.</p>}
      </section>
      <section aria-label="Applies to" className="space-y-1">
        <h3 className="dash-label font-medium text-fg-muted">Applies to</h3>
        {agents ? (
          agents.length ? (
            <ul className="dash-body text-fg">
              {agents.map((a) => (
                <li key={a.id}>
                  <Link className="underline" to={`/builder/agents/${encodeURIComponent(a.id)}`}>
                    {a.id}
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="dash-body text-fg-faint">No agents use this policy.</p>
          )
        ) : (
          <p className="dash-body text-fg">{it.used_by_count ?? NOT_REPORTED} agents</p>
        )}
      </section>
    </>
  )
}

/** Who a part reaches: read before changing it. Agents link to their detail. */
function UsedBy({ kind, id }: { kind: LibraryKind; id: string }) {
  const [load, retry] = useLoad(() => builderApi.libraryUsedBy(kind, id), [kind, id])
  if (load.status === 'loading') return <div aria-busy="true" className="h-10 animate-pulse bg-surface" />
  if (load.status === 'error')
    return (
      <p role="alert" className="dash-support text-fg-secondary">
        Used-by could not be read.{' '}
        <button type="button" className="underline" onClick={retry}>
          Retry
        </button>
      </p>
    )
  const { agents, launches, projects } = load.data
  const groups = [
    ['Agents', agents.map((a) => ({ key: a.id, text: a.name || a.id, to: `/builder/agents/${encodeURIComponent(a.id)}` }))],
    ['Launches', launches.map((l) => ({ key: l.id, text: l.id, to: undefined }))],
    ['Projects', projects.map((p) => ({ key: p.id, text: p.id, to: undefined }))],
  ] as const
  if (groups.every(([, rows]) => rows.length === 0)) return <p className="dash-body text-fg-faint">Nothing uses this yet.</p>
  return (
    <div className="space-y-2">
      {groups
        .filter(([, rows]) => rows.length > 0)
        .map(([title, rows]) => (
          <div key={title}>
            <h4 className="dash-support text-fg-muted">{title}</h4>
            <ul className="dash-body text-fg">
              {rows.map((r) => (
                <li key={r.key}>{r.to ? <Link className="underline" to={r.to}>{r.text}</Link> : r.text}</li>
              ))}
            </ul>
          </div>
        ))}
    </div>
  )
}

function Detail({ kind, id }: { kind: LibraryKind; id: string }) {
  const [load, retry] = useLoad(() => builderApi.libraryItem(kind, id), [kind, id])
  const { guard, dialog: discardDialog } = useDiscardGuard()
  const [mode, recheckMode] = useChangeMode()
  const [prepared, setPrepared] = useState<string>()
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
    setPrepared(undefined)
    retry()
  }, [retry])
  if (load.status === 'loading') return <div aria-busy="true" className="m-4 h-40 animate-pulse bg-surface" />
  if (load.status === 'error')
    return (
      <div role="alert" className="space-y-2 p-4">
        <p className="dash-body text-fg-secondary">{load.message}</p>
        <Button type="button" onClick={retry} variant="outline" size="sm">
          Retry
        </Button>
      </div>
    )
  const it = load.data
  const partKind: PartKind | null = kind === 'prompts' || kind === 'policies' ? kind : null
  const canEdit = partKind !== null && it.editable !== false
  const fields = partKind ? PART_FIELDS[partKind] : []
  const dirty = partKind ? changedPartPaths(partKind, it, draft) : []
  const body = typeof it.body === 'string' ? it.body : null
  const viewPrepared = async () => {
    if (!prepared) return
    setBusy(true)
    setError('')
    try {
      setReview(await builderApi.change(prepared))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
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
        {canEdit && !editing && mode !== 'unavailable' && (
          <Button type="button" variant="outline" size="sm" onClick={() => setEditing(true)}>
            Edit
          </Button>
        )}
      </div>
      {canEdit && mode === 'unavailable' && <ModeNote onCheck={recheckMode} />}
      {!canEdit && (kind === 'skills' || kind === 'tool_servers') && <p className="dash-support text-fg-muted">Read-only: managed in files.</p>}
      {editing && (
        <div role="status" className="flex flex-wrap items-center gap-3 border border-border bg-surface px-3 py-2 dash-support text-fg-secondary">
          {prepared ? <PreparedLabel /> : <span className="mr-auto">{changingText(dirty.map((d) => fields.find((f) => f.path === d)?.label))}</span>}
          {error && (
            <span role="alert" className="text-danger">
              {error}
            </span>
          )}
          {!prepared && <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => guard(dirty.length, () => {
              setEditing(false)
              setDraft({})
              setError('')
            })}
          >
            Discard
          </Button>}
          <Button type="button" disabled={(dirty.length === 0 && !prepared) || busy} variant="default" size="sm" onClick={prepared ? viewPrepared : openReview}>
            {prepared ? 'View hand-off' : busy ? (mode === 'handoff' ? 'Preparing' : 'Checking') : mode === 'handoff' ? 'Prepare change' : 'Review changes'}
          </Button>
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
                  <Textarea id={`lib-${f.path}`} className="font-mono" rows={14} value={String(value)} onChange={(e) => set(e.target.value)} />
                ) : f.kind === 'chips' ? (
                  <Input
                    id={`lib-${f.path}`}
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
                  <Input id={`lib-${f.path}`} value={String(value)} onChange={(e) => set(e.target.value)} />
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
              <Button type="button" variant="outline" size="sm" aria-pressed={raw} onClick={() => setRaw((r) => !r)}>
                {raw ? 'Rendered' : 'Raw'}
              </Button>
              {raw ? (
                <pre className="max-h-[32rem] overflow-auto bg-surface p-3 font-mono dash-support text-fg">{body}</pre>
              ) : (
                <div className="prose-sm max-w-none dash-body text-fg">
                  <Markdown>{body}</Markdown>
                </div>
              )}
            </section>
          )}
          {kind === 'policies' && <PolicyLead it={it} />}
          {kind !== 'policies' && (
            <section aria-label="Used by" className="space-y-1">
              <h3 className="dash-label font-medium text-fg-muted">Used by</h3>
              <UsedBy kind={kind} id={id} />
            </section>
          )}
          {kind === 'policies' && <h3 className="dash-label font-medium text-fg-muted">Details</h3>}
          <dl>
            {Object.entries(it)
              .filter(([k]) => !SKIP_NOW.has(k) && !(kind === 'policies' && POLICY_LEAD.has(k)))
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
      {discardDialog}
      {review && (
        <ReviewDialog
          initial={review}
          onClose={(c) => {
            setReview(null)
            setPrepared(mode === 'handoff' && ['awaiting_handoff', 'awaiting_approval', 'stale'].includes(c.state) ? c.change_id : undefined)
          }}
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
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search library" aria-label="Search library" />
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
                <Button type="button" onClick={retry} variant="outline" size="sm">
                  Retry
                </Button>
              </div>
            )}
            {load.status === 'ready' && items.length === 0 && <p className="p-4 dash-body text-fg-faint">
                {debounced ? 'No matches.' : `No ${(LIBRARY_KINDS.find((k) => k.id === kind)?.label ?? 'items').toLowerCase()} in the catalog yet.`}
              </p>}
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
