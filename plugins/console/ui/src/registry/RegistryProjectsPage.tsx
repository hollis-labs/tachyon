import { type ReactNode, useEffect, useRef, useState } from 'react'
import { Button, ConfirmDialog, Input, SearchInput, Textarea } from '@hollis-labs/design-components'
import { useNavigate, useParams } from 'react-router-dom'
import { Chip } from '../components/chip'
import { ChipListField } from '../builder/ChipListField'
import { Cap, Field as Row, Tile } from '../builder/parts'
import { useLoad } from '../builder/useLoad'
import { BuilderError, builderApi, type Change } from '../builder/api'
import { useChangeMode } from '../builder/capabilities'
import { newRequestId, type ReviewRow } from '../builder/changes'
import { DirtyBar } from '../builder/DirtyBar'
import { ModeNote } from '../builder/ModeNote'
import { ReviewDialog } from '../builder/ReviewDialog'
import { useDiscardGuard } from '../builder/DiscardGuard'
import { NOT_REPORTED } from '../builder/vocab'
import { type Project, type ProjectPatch, type ProjectRow, type ProjectStatus, errorMessage, profilePatch, registryApi } from './api'

const STATUS: Record<string, string> = { active: 'Active', paused: 'Paused', archived: 'Archived' }
const FIELD_LABEL: Record<string, string> = { display_name: 'name', description: 'description', tags: 'tags', guidelines: 'guidelines', entry_points: 'entry points', status: 'status' }
const fieldList = (keys: string[]) => keys.map((k) => FIELD_LABEL[k] ?? k).join(', ')


const UNSAVED = 'Your edits have not been saved and will be lost.'

/** Inline value: reads as text, edits in place. */
const BARE = 'h-auto w-full border-0 bg-transparent px-0 py-0 text-control shadow-none focus-visible:ring-0'
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

function ProfileDetail({ id, onBack, dirtyRef }: { id: string; onBack: () => void; dirtyRef: { current: number } }) {
  const [load, retry] = useLoad(() => registryApi.get(id), [id])
  const { guard, dialog: discardDialog } = useDiscardGuard(UNSAVED)
  const [draft, setDraft] = useState<ProjectPatch>({})
  const [saved, setSaved] = useState<Project | null>(null)
  const [savedFields, setSavedFields] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [noWrite, setNoWrite] = useState(false)
  const [archiving, setArchiving] = useState(false)
  const [mode, recheckMode] = useChangeMode()
  const [servers, setServers] = useState<string[] | null>(null)
  const [review, setReview] = useState<Change | null>(null)
  const [prepared, setPrepared] = useState<string>()
  const [cbusy, setCbusy] = useState(false)
  useEffect(() => {
    setDraft({})
    setSaved(null)
    setError('')
    setServers(null)
    setReview(null)
    setPrepared(undefined)
  }, [id])

  if (load.status === 'loading') return <div aria-busy="true" className="m-5 h-40 animate-pulse rounded-control bg-surface" />
  if (load.status === 'error')
    return (
      <div role="alert" className="space-y-2 p-5">
        <p className="text-control text-fg-secondary">{load.message}</p>
        <Button type="button" onClick={retry} variant="outline" size="sm">
          Retry
        </Button>
      </div>
    )
  const p: Project = saved ?? load.data
  const patch = profilePatch(p, draft)
  const dirty = Object.keys(patch).length
  dirtyRef.current = dirty
  const val = <K extends keyof ProjectPatch>(k: K, fallback: NonNullable<ProjectPatch[K]>) => (draft[k] ?? fallback) as NonNullable<ProjectPatch[K]>
  const set = (v: ProjectPatch) => setDraft((d) => ({ ...d, ...v }))
  const send = async (body: ProjectPatch) => {
    setBusy(true)
    setError('')
    try {
      const fields = fieldList(Object.keys(body))
      setSaved(await registryApi.patch(p.id, body))
      setSavedFields(fields)
      setDraft({})
      return true
    } catch (e) {
      // The real registry lives in Tether; until the host holds its control token the write route answers 404/405/501.
      const unsupported = e instanceof BuilderError && [404, 405, 501].includes(e.status)
      if (unsupported) setNoWrite(true)
      setError(unsupported ? 'Saving the registry profile needs Tether write access.' : e instanceof BuilderError && e.code === 'assurance_insufficient' ? 'Saving needs a stronger sign-in. Step up your access, then save again.' : errorMessage(e))
      return false
    } finally {
      setBusy(false)
    }
  }
  const save = () => void send(patch)
  const c = p.consumed_by
  const archived = p.identity.status === 'archived'
  const current = p.catalog?.mcp_servers ?? null
  const serversChanged = servers !== null && JSON.stringify(servers) !== JSON.stringify(current ?? [])
  dirtyRef.current = dirty + (serversChanged ? 1 : 0)
  const canCatalog = p.link.state === 'linked' && p.link.catalog_id != null && mode !== 'unavailable'
  const rows: ReviewRow[] = serversChanged ? [{ label: 'Tool servers', from: (current ?? []).join(', ') || 'inherited (torque only)', to: (servers ?? []).join(', ') || 'empty' }] : []
  const openReview = async () => {
    setCbusy(true)
    setError('')
    try {
      setReview(
        await builderApi.createChange({
          request_id: newRequestId(),
          ops: [{ target: { kind: 'project', id: p.link.catalog_id ?? '' }, expected_sha: p.catalog?.sha ?? '', op: { type: 'set', path: 'mcp.servers', value: servers ?? [] } }],
        }),
      )
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setCbusy(false)
    }
  }
  const viewPrepared = async () => {
    if (!prepared) return
    setCbusy(true)
    try {
      setReview(await builderApi.change(prepared))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setCbusy(false)
    }
  }
  return (
    <div
      className="flex min-h-0 flex-1 flex-col"
      onKeyDown={(e) => {
        if (dirty > 0 && !busy && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
          e.preventDefault()
          save()
        }
      }}
    >
      <header className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border-subtle px-4 py-3">
        <Button type="button" variant="outline" size="sm" onClick={onBack} className="sm:hidden">
          ‹ Projects
        </Button>
        <Tile name={p.identity.display_name || p.id} large />
        <div className="flex min-w-0 basis-full flex-col sm:flex-1 sm:basis-0">
          <h2 className="truncate font-sans text-base font-semibold normal-case tracking-normal text-fg">{p.identity.display_name || p.id}</h2>
          <p className="truncate font-mono text-label text-fg-muted">
            {p.id} · {STATUS[p.identity.status]?.toLowerCase() ?? p.identity.status}
          </p>
        </div>
        {p.link.state === 'unlinked' && <Chip>unlinked</Chip>}
        {dirty > 0 && (
          <Button type="button" variant="outline" size="sm" onClick={() => guard(dirty, () => { setDraft({}); setError('') })}>
            Discard
          </Button>
        )}
        {!archived && (
          <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => setArchiving(true)}>
            Archive
          </Button>
        )}
        <Button type="button" size="sm" disabled={dirty === 0 || busy || noWrite} title={noWrite ? 'Needs Tether write access' : undefined} onClick={save}>
          {busy ? 'Saving' : error ? 'Retry' : 'Save'}
        </Button>
      </header>
      {discardDialog}
      <ConfirmDialog
        open={archiving}
        onOpenChange={setArchiving}
        title={`Archive ${p.identity.display_name || p.id}?`}
        description="It leaves the Active list. Agents that already read it keep working, and you can set it active again later."
        confirmLabel="Archive"
        destructive={false}
        busy={busy}
        onConfirm={async () => {
          if (await send({ status: 'archived' })) setArchiving(false)
        }}
      />
      {(dirty > 0 || error || (saved && dirty === 0)) && (
        <p role={error ? 'alert' : 'status'} className={`border-b border-border-subtle px-4 py-2 text-xs ${error ? 'text-danger' : 'text-fg-secondary'}`}>
          {error ? error : dirty > 0 ? <><b className="font-semibold text-fg">{plural(dirty, 'unsaved change')}</b> · {fieldList(Object.keys(patch))}</> : `Saved ${savedFields}. Registry profile changes apply immediately.`}
        </p>
      )}
      <div className="flex min-h-0 flex-1 flex-col gap-[22px] overflow-auto px-4 py-4 [&>*]:flex-none">
        <section aria-label="Identity" className="flex flex-col gap-2">
          <Cap>Identity</Cap>
          <dl className="flex flex-col gap-2">
            <Row label="Name">
              <Input aria-label="Name" maxLength={120} className={BARE} value={val('display_name', p.identity.display_name)} onChange={(e) => set({ display_name: e.target.value })} />
            </Row>
            <Row label="Description">
              <Input aria-label="Description" maxLength={2000} className={BARE} placeholder="none" value={val('description', p.identity.description)} onChange={(e) => set({ description: e.target.value })} />
            </Row>
            <Row label="Tags">
              <ChipListField label="Tags" editable value={val('tags', p.identity.tags)} onChange={(next) => set({ tags: next })} />
            </Row>
          </dl>
        </section>
        <section aria-label="Guidance" className="flex flex-col gap-2">
          <Cap>Guidance</Cap>
          <dl className="flex flex-col gap-2">
            <Row label="Entry points">
              <ChipListField label="Entry points" editable value={val('entry_points', p.guidance.entry_points)} onChange={(next) => set({ entry_points: next })} />
            </Row>
          </dl>
          <Textarea aria-label="Guidelines" rows={6} maxLength={8000} placeholder="Shared guidelines every agent in this project reads at boot." className="font-mono text-xs" value={val('guidelines', p.guidance.guidelines)} onChange={(e) => set({ guidelines: e.target.value })} />
        </section>
        <section aria-label="Tool servers" className="flex flex-col gap-2">
          <Cap>Tool servers</Cap>
          {p.link.state === 'unlinked' ? (
            <p className="text-control text-fg-secondary">Not linked to a catalog project{p.link.reason ? `: ${p.link.reason}` : '.'}</p>
          ) : (
            <dl className="flex flex-col gap-2">
              <Row label="Servers">
                <ChipListField label="Tool servers" editable={canCatalog} value={servers ?? current ?? []} onChange={setServers} empty={current == null ? 'inherited' : 'none granted'} />
              </Row>
              {p.catalog?.repo_root && <Row label="Repository"><span className="font-mono text-xs">{p.catalog.repo_root}</span></Row>}
            </dl>
          )}
          <p className="text-label text-fg-muted">A non-empty list replaces the default (torque only). Changing them is a reviewed change, applied by the Builder save mode.</p>
        </section>
        <section aria-label="Consumed by" className="flex flex-col gap-2">
          <Cap>Consumed by</Cap>
          <ul className="flex flex-wrap gap-1.5">
            {c?.agents != null && <li><Chip>{plural(c.agents.length, 'agent')}</Chip></li>}
            {c?.launches != null && <li><Chip>{plural(c.launches.length, 'launch', 'launches')}</Chip></li>}
            {c?.planner_items != null && <li><Chip>{plural(c.planner_items.open, 'open planner item')}</Chip></li>}
            {c?.tether_sessions != null && <li><Chip>{c.tether_sessions.live} live {c.tether_sessions.live === 1 ? 'session' : 'sessions'}</Chip></li>}
            {!c?.agents && !c?.launches && <li className="text-xs text-fg-muted">{NOT_REPORTED}</li>}
          </ul>
        </section>
      </div>
      {mode === 'unavailable' && <ModeNote onCheck={recheckMode} />}
      {(serversChanged || prepared) && (
        <DirtyBar
          labels={rows.map((r) => r.label)}
          prepared={Boolean(prepared)}
          busy={cbusy}
          mode={mode}
          onDiscard={() => setServers(null)}
          onReview={openReview}
          onViewPrepared={viewPrepared}
        />
      )}
      {review && (
        <ReviewDialog
          initial={review}
          subject={p.identity.display_name || p.id}
          rows={rows}
          onClose={(c) => {
            setReview(null)
            setPrepared(mode === 'handoff' && ['awaiting_handoff', 'awaiting_approval', 'stale'].includes(c.state) ? c.change_id : undefined)
          }}
          onApplied={() => {
            setServers(null)
            setPrepared(undefined)
            retry()
          }}
          onReload={() => {
            setReview(null)
            setServers(null)
            retry()
          }}
        />
      )}
    </div>
  )
}

/** Registry projects: list and profile. Profile edits write directly (operator session); catalog tool servers go through Builder changes. */
export function RegistryProjectsPage() {
  const { id } = useParams()
  const rawNavigate = useNavigate()
  const dirtyRef = useRef(0)
  const { guard, dialog: leaveDialog } = useDiscardGuard(UNSAVED)
  // Moving away from a profile with unsaved edits asks first; the auto-select replace does not.
  const navigate = (to: string, opts?: { replace?: boolean }) =>
    opts?.replace
      ? rawNavigate(to, opts)
      : guard(dirtyRef.current, () => {
          dirtyRef.current = 0
          rawNavigate(to, opts)
        })
  const [q, setQ] = useState('')
  const [load, retry] = useLoad(() => registryApi.list(q), [q])
  const [activeOnly, setActiveOnly] = useState(true)
  const [unlinkedOnly, setUnlinkedOnly] = useState(false)
  const all: ProjectRow[] = load.status === 'ready' ? load.data.projects : []
  const rows = all.filter((r) => (!activeOnly || r.status === 'active') && (!unlinkedOnly || r.link.state !== 'linked'))
  const anyUnlinked = all.some((r) => r.link.state !== 'linked')
  useEffect(() => {
    if (!id && rows.length > 0 && window.matchMedia?.('(min-width: 641px)').matches) rawNavigate(`/registry/projects/${encodeURIComponent(rows[0].id)}`, { replace: true })
  }, [id, rows, rawNavigate])
  const pill = (on: boolean) => `inline-flex h-5 items-center rounded-full border border-border px-2 text-label ${on ? 'bg-selection text-fg' : 'text-fg-secondary hover:text-fg'}`
  return (
    <div className="flex h-full min-w-0 flex-col">
      {leaveDialog}
      <header className="flex h-12 shrink-0 items-center gap-3 overflow-x-auto border-b border-border-subtle px-4">
        <h1 className="font-mono text-caption font-semibold uppercase tracking-label text-fg">Registry</h1>
        <nav aria-label="Registry areas" className="flex shrink-0 gap-0.5">
          <span aria-current="page" className="inline-flex h-7 items-center gap-1.5 rounded-control bg-selection px-3 text-xs font-semibold text-fg">
            Projects
            {load.status === 'ready' && <small className="font-mono text-caption text-fg-faint">{all.length}</small>}
          </span>
          {['Machines', 'Services', 'Repos'].map((n) => (
            <span key={n} aria-disabled="true" className="inline-flex h-7 items-center gap-1.5 rounded-control px-3 text-xs text-fg-muted">
              {n}
              <small className="font-mono text-caption text-fg-faint">soon</small>
            </span>
          ))}
        </nav>
        <span className="flex-1" />
        <p className="hidden shrink-0 text-label text-fg-muted sm:block">Shared data · used by Tachyon and other apps</p>
        <Button type="button" size="sm" disabled title="Creating a project arrives with the registry write path" className="hidden sm:inline-flex">
          New project
        </Button>
      </header>
      <div className="flex min-h-0 flex-1">
        <div className={`${id ? 'hidden sm:flex' : 'flex'} w-full shrink-0 flex-col border-r border-border-subtle sm:w-[340px]`}>
          <div className="flex items-center gap-2 p-2">
            <div className="dash-search-adapter min-w-0 flex-1 [&_div]:min-w-0">
              <SearchInput value={q} onChange={setQ} placeholder="Search projects" ariaLabel="Search projects" />
            </div>
            <button type="button" aria-pressed={activeOnly} onClick={() => setActiveOnly((v) => !v)} className={pill(activeOnly)}>
              Active
            </button>
            {anyUnlinked && (
              <button type="button" aria-pressed={unlinkedOnly} onClick={() => setUnlinkedOnly((v) => !v)} className={pill(unlinkedOnly)}>
                Not linked
              </button>
            )}
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
            {load.status === 'ready' && rows.length === 0 && (
              <p className="p-4 text-control text-fg-faint">{all.length === 0 && !q ? 'No projects in the registry yet.' : 'No projects match. Clear the search or filters.'}</p>
            )}
            <ul aria-label="Projects">
              {rows.map((r) => (
                <li key={r.id} className="border-t border-border-subtle first:border-t-0">
                  <button
                    type="button"
                    aria-current={r.id === id ? 'true' : undefined}
                    onClick={() => navigate(`/registry/projects/${encodeURIComponent(r.id)}`)}
                    className={`flex w-full items-center gap-3 px-3.5 py-2.5 text-left hover:bg-selection ${r.id === id ? 'bg-selection' : ''}`}
                  >
                    <Tile name={r.display_name || r.id} />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <b className="truncate text-control font-semibold text-fg">{r.display_name || r.id}</b>
                      <span className="truncate font-mono text-label text-fg-muted">{r.id}</span>
                    </span>
                    {r.link.state !== 'linked' && <Chip>unlinked</Chip>}
                    {r.counts?.agents != null && <span className="shrink-0 font-mono text-label text-fg-muted">{plural(r.counts.agents, 'agent')}</span>}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        </div>
        <div className={`${id ? 'flex' : 'hidden sm:flex'} min-w-0 flex-1 flex-col`}>
          {id ? <ProfileDetail id={decodeURIComponent(id)} dirtyRef={dirtyRef} onBack={() => navigate('/registry/projects')} /> : <p className="p-5 text-control text-fg-faint">Select a project.</p>}
        </div>
      </div>
    </div>
  )
}
