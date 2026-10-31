import { useDiscardGuard } from './DiscardGuard'
import { type ReactNode, useCallback, useEffect, useState } from 'react'
import { Button, Input, SearchInput, Textarea } from '@hollis-labs/design-components'
import Markdown from 'react-markdown'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Chip } from '../components/chip'
import { ChipListField } from './ChipListField'
import { DirtyBar } from './DirtyBar'
import { Cap, Muted } from './parts'
import { BuilderShell } from './BuilderShell'
import { type Change, type LibraryItem, type LibraryKind, builderApi, errorMessage } from './api'
import { type Draft, PART_FIELDS, type PartKind, buildPartOps, changedPartPaths, newRequestId, partRead } from './changes'
import { useChangeMode } from './capabilities'
import { ModeNote } from './ModeNote'
import { ReviewDialog } from './ReviewDialog'
import { useLoad } from './useLoad'
import { LIBRARY_KINDS, NOT_REPORTED } from './vocab'

const SKIP = new Set(['updated_at', 'archived', 'sha', 'kind', 'id', 'name', 'summary', 'used_by_count', 'editable', 'body'])
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

const GRID = 'grid gap-0.5 py-[3px] sm:grid-cols-[130px_1fr] sm:gap-3'

const POLICY_LEAD = new Set(['permission_mode', 'network', 'default_sandbox', 'mcp_scopes', 'rules', 'scope_count', 'privileged', 'bundle', 'referencing_agents'])
const WORD = (v: unknown) => (typeof v === 'string' && v ? v : NOT_REPORTED)

/** A policy is a bundle of settings an agent takes on, then the agents it applies to. */
function PolicyLead({ it }: { it: LibraryItem }) {
  const bundle = isObj(it.bundle) ? it.bundle : {}
  const val = (k: string) => (k in bundle ? bundle[k] : it[k])
  const scopes = Array.isArray(val('mcp_scopes')) ? (val('mcp_scopes') as unknown[]).map(String) : null
  const rules = Array.isArray(val('rules')) ? (val('rules') as unknown[]).map(String) : null
  const agents = Array.isArray(it.referencing_agents) ? (it.referencing_agents as { id: string }[]) : null
  return (
    <>
      <section aria-label="Settings" className="space-y-1">
        <Cap>Settings</Cap>
        <dl>
          {(
            [
              ['Permission mode', WORD(val('permission_mode'))],
              ['Network', WORD(val('network'))],
              ['Sandbox', WORD(val('default_sandbox'))],
              ['Scopes', scopes === null ? (typeof it.scope_count === 'number' ? `${it.scope_count} scopes` : NOT_REPORTED) : scopes.length ? scopes.join(', ') : 'none'],
              ['Rules', rules === null ? NOT_REPORTED : rules.length ? rules.join(', ') : 'none'],
            ] as const
          ).map(([k, v]) => (
            <div key={k} className={GRID}>
              <dt className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-1">{k}</dt>
              <dd className="text-control text-fg [overflow-wrap:anywhere]">{v}</dd>
            </div>
          ))}
        </dl>
        {it.privileged === true && <p className="dash-support text-fg-secondary">Includes privileged settings. Changing them needs a confirmation in the Review step.</p>}
      </section>
      <section aria-label="Applies to" className="space-y-1">
        <Cap>Applies to</Cap>
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
  const chips = [
    ...agents.map((a) => ({ key: `a:${a.id}`, text: a.name || a.id, to: `/builder/agents/${encodeURIComponent(a.id)}` })),
    ...launches.map((l) => ({ key: `l:${l.id}`, text: l.id, to: '/builder/launches' })),
    ...projects.map((p) => ({ key: `p:${p.id}`, text: p.id, to: `/registry/projects/${encodeURIComponent(p.id)}` })),
  ]
  if (chips.length === 0) return <p className="text-control text-fg-faint">Nothing uses this yet.</p>
  const shown = chips.slice(0, 3)
  return (
    <ul className="flex flex-wrap gap-1.5">
      {shown.map((c) => (
        <li key={c.key}>
          <Link to={c.to} className="inline-flex h-5 items-center rounded border border-border px-1.5 font-mono text-caption text-fg-secondary hover:bg-selection hover:text-fg">
            {c.text}
          </Link>
        </li>
      ))}
      {chips.length > shown.length && (
        <li>
          <Chip>+{chips.length - shown.length}</Chip>
        </li>
      )}
    </ul>
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
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border-subtle px-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col">
          <h2 className="truncate font-mono text-base font-semibold normal-case tracking-normal text-fg">{it.name}</h2>
          <span className="text-label text-fg-muted">Library · {LIBRARY_KINDS.find((k) => k.id === kind)?.label}</span>
        </div>
        {canEdit && !editing && mode !== 'unavailable' && (
          <Button type="button" variant="outline" size="sm" onClick={() => setEditing(true)}>
            Edit
          </Button>
        )}
        <Button type="button" variant="outline" size="sm" disabled title="Duplicating a part arrives with the part editor">
          Duplicate
        </Button>
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-4">
      {canEdit && mode === 'unavailable' && <ModeNote onCheck={recheckMode} />}
      {!canEdit && (kind === 'skills' || kind === 'tool_servers') && <Muted>Read-only: managed in files.</Muted>}
      {editing && partKind ? (
        <div>
          {fields.map((f) => {
            const value = f.path in draft ? draft[f.path] : partRead(it, f)
            const set = (v: unknown) => setDraft((d) => ({ ...d, [f.path]: v }))
            return (
              <div key={f.path} className={GRID}>
                <label htmlFor={`lib-${f.path}`} className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-2">
                  {f.label}
                </label>
                {f.kind === 'longtext' ? (
                  <Textarea id={`lib-${f.path}`} className="font-mono" rows={14} value={String(value)} onChange={(e) => set(e.target.value)} />
                ) : f.kind === 'chips' ? (
                  <ChipListField label={f.label} editable value={value as string[]} onChange={set} />
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
            <section aria-label="Used by" className="flex flex-col gap-2">
              <Cap>Used by</Cap>
              <UsedBy kind={kind} id={id} />
              <Muted>Changing this updates all agents that reference it. Review shows each affected agent.</Muted>
            </section>
          )}
          {kind === 'policies' && <Cap>Details</Cap>}
          <dl>
            {Object.entries(it)
              .filter(([k]) => !SKIP_NOW.has(k) && !(kind === 'policies' && POLICY_LEAD.has(k)))
              .map(([k, v]) => (
                <div key={k} className={GRID}>
                  <dt className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-1">{label(k)}</dt>
                  <dd className="min-w-0 text-control text-fg [overflow-wrap:anywhere]">
                    <Value v={v} />
                  </dd>
                </div>
              ))}
          </dl>
        </>
      )}
      </div>
      {editing && (
        <DirtyBar
          labels={dirty.map((d) => fields.find((f) => f.path === d)?.label)}
          prepared={Boolean(prepared)}
          error={error}
          busy={busy}
          mode={mode}
          onDiscard={() =>
            guard(dirty.length, () => {
              setEditing(false)
              setDraft({})
              setError('')
            })
          }
          onReview={openReview}
          onViewPrepared={viewPrepared}
        />
      )}
      {discardDialog}
      {review && (
        <ReviewDialog
          initial={review}
          subject={it.name}
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

/** Library of prompts, skills, tool servers and policies. */
export function BuilderLibraryPage() {
  const { kind: rawKind = 'prompts', id } = useParams()
  const navigate = useNavigate()
  const kind = (LIBRARY_KINDS.find((k) => k.id === rawKind)?.id ?? 'prompts') as LibraryKind
  const [q, setQ] = useState('')
  const [summary] = useLoad(builderApi.summary, [])
  const [load, retry] = useLoad(() => builderApi.library(kind, q), [kind, q])
  const items: LibraryItem[] = load.status === 'ready' ? load.data.items : []
  const lib = summary.status === 'ready' ? summary.data.library : undefined
  const kindLabel = LIBRARY_KINDS.find((k) => k.id === kind)?.label ?? 'items'
  return (
    <div className="flex h-full min-w-0 flex-col">
      <BuilderShell tab="library" counts={summary.status === 'ready' ? summary.data : null} />
      <nav aria-label="Library kinds" className="flex shrink-0 gap-0.5 overflow-x-auto border-b border-border-subtle px-3 py-1.5">
        {LIBRARY_KINDS.map((k) => {
          const n = lib?.[k.id]
          return (
            <Link
              key={k.id}
              to={`/builder/library/${k.id}`}
              aria-current={k.id === kind ? 'page' : undefined}
              className={`inline-flex h-[26px] shrink-0 items-center gap-1.5 rounded-full px-3 text-xs ${k.id === kind ? 'border border-border bg-selection font-semibold text-fg' : 'text-fg-muted hover:text-fg'}`}
            >
              {k.label}
              {n != null && <small className="font-mono text-caption text-fg-faint">{n}</small>}
            </Link>
          )
        })}
      </nav>
      <div className="flex min-h-0 flex-1">
        <div className={`${id ? 'hidden sm:flex' : 'flex'} w-full shrink-0 flex-col border-r border-border-subtle sm:w-[340px]`}>
          <div className="dash-search-adapter p-2 [&_div]:min-w-0">
            <SearchInput value={q} onChange={setQ} placeholder={`Search ${kindLabel.toLowerCase()}`} ariaLabel="Search library" />
          </div>
          <div className="min-h-0 flex-1 overflow-auto border-t border-border-subtle">
            {load.status === 'loading' && (
              <div aria-busy="true" className="space-y-2 p-4">
                {[0, 1, 2].map((i) => (
                  <div key={i} className="h-10 animate-pulse rounded-control bg-surface" />
                ))}
              </div>
            )}
            {load.status === 'error' && (
              <div role="alert" className="space-y-2 p-4">
                <p className="text-control text-fg-secondary">{load.message}</p>
                <Button type="button" onClick={retry} variant="outline" size="sm">
                  Retry
                </Button>
              </div>
            )}
            {load.status === 'ready' && items.length === 0 && (
              <p className="p-4 text-control text-fg-faint">{q ? 'No matches.' : `No ${kindLabel.toLowerCase()} in the catalog yet.`}</p>
            )}
            <ul aria-label="Library items">
              {items.map((it) => (
                <li key={it.id} className="border-t border-border-subtle first:border-t-0">
                  <button
                    type="button"
                    aria-current={it.id === id ? 'true' : undefined}
                    onClick={() => navigate(`/builder/library/${kind}/${encodeURIComponent(it.id)}`)}
                    className={`flex w-full flex-col px-3.5 py-2.5 text-left hover:bg-selection ${it.id === id ? 'bg-selection' : ''}`}
                  >
                    <b className="truncate font-mono text-control font-semibold text-fg">{it.name}</b>
                    <span className="truncate text-label text-fg-muted">
                      {[it.summary, it.used_by_count != null ? `used by ${it.used_by_count} ${it.used_by_count === 1 ? 'agent' : 'agents'}` : ''].filter(Boolean).join(' · ')}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        </div>
        <div className={`${id ? 'flex' : 'hidden sm:flex'} min-w-0 flex-1 flex-col`}>
          {id ? (
            <>
              <Link to={`/builder/library/${kind}`} className="px-4 pt-3 text-control text-fg-muted hover:text-fg sm:hidden">
                ‹ {kindLabel}
              </Link>
              <Detail kind={kind} id={decodeURIComponent(id)} />
            </>
          ) : (
            <p className="p-5 text-control text-fg-faint">Select an item to read it.</p>
          )}
        </div>
      </div>
    </div>
  )
}
