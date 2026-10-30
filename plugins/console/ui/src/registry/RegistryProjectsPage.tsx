import { type ReactNode, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useLoad } from '../builder/useLoad'
import { BuilderError } from '../builder/api'
import { confirmDiscard } from '../builder/changes'
import { NOT_REPORTED } from '../builder/vocab'
import { type Project, type ProjectPatch, type ProjectRow, type ProjectStatus, errorMessage, profilePatch, registryApi } from './api'

const BTN = 'border border-border px-3 py-1 dash-control text-fg-secondary hover:bg-surface-hover disabled:opacity-50'
const INPUT = 'w-full border border-border bg-bg px-2 py-1 dash-body text-fg'
const STATUS: Record<string, string> = { active: 'Active', paused: 'Paused', archived: 'Archived' }
const FIELD_LABEL: Record<string, string> = { display_name: 'name', description: 'description', tags: 'tags', guidelines: 'guidelines', entry_points: 'entry points', status: 'status' }
const fieldList = (keys: string[]) => keys.map((k) => FIELD_LABEL[k] ?? k).join(', ')
const lines = (v: string) =>
  v
    .split('\n')
    .map((x) => x.trim())
    .filter(Boolean)
const tags = (v: string) =>
  v
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean)

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1 border-b border-border-subtle py-2 md:grid-cols-[10rem_1fr] md:gap-3">
      <dt className="dash-support text-fg-muted">{label}</dt>
      <dd className="min-w-0 dash-body text-fg [overflow-wrap:anywhere]">{children}</dd>
    </div>
  )
}

const count = (v: number | null | undefined) => (v == null ? NOT_REPORTED : String(v))

function ProfileDetail({ id, onBack }: { id: string; onBack: () => void }) {
  const [load, retry] = useLoad(() => registryApi.get(id), [id])
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<ProjectPatch>({})
  const [saved, setSaved] = useState<Project | null>(null)
  const [savedFields, setSavedFields] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    setEditing(false)
    setDraft({})
    setSaved(null)
    setError('')
  }, [id])

  if (load.status === 'loading') return <div aria-busy="true" className="m-5 h-40 animate-pulse bg-surface" />
  if (load.status === 'error')
    return (
      <div role="alert" className="space-y-2 p-5">
        <p className="dash-body text-fg-secondary">{load.message}</p>
        <button type="button" onClick={retry} className={BTN}>
          Retry
        </button>
      </div>
    )
  const p: Project = saved ?? load.data
  const patch = profilePatch(p, draft)
  const dirty = Object.keys(patch).length
  const val = <K extends keyof ProjectPatch>(k: K, fallback: NonNullable<ProjectPatch[K]>) => (draft[k] ?? fallback) as NonNullable<ProjectPatch[K]>
  const set = (v: ProjectPatch) => setDraft((d) => ({ ...d, ...v }))
  const save = async () => {
    setBusy(true)
    setError('')
    try {
      const fields = fieldList(Object.keys(patch))
      setSaved(await registryApi.patch(p.id, patch))
      setSavedFields(fields)
      setDraft({})
      setEditing(false)
    } catch (e) {
      setError(e instanceof BuilderError && e.code === 'assurance_insufficient' ? 'Saving needs a stronger sign-in. Step up your access, then save again.' : errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const c = p.consumed_by

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-auto">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
        <button type="button" onClick={onBack} className="dash-control text-fg-muted hover:text-fg md:hidden">
          ← Projects
        </button>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-lg font-semibold text-fg">{p.identity.display_name || p.id}</h2>
          <p className="truncate font-mono dash-support text-fg-muted">{p.id}</p>
        </div>
        {!editing && (
          <button type="button" className={BTN} onClick={() => setEditing(true)}>
            Edit profile
          </button>
        )}
      </header>
      {editing && (
        <div role="status" className="flex flex-wrap items-center gap-3 border-b border-border bg-surface px-5 py-2 dash-support text-fg-secondary">
          <span className="mr-auto">{dirty === 0 ? 'No unsaved changes' : `Changing ${dirty} field${dirty === 1 ? '' : 's'}: ${fieldList(Object.keys(patch))}`}</span>
          {error && (
            <span role="alert" className="text-danger">
              {error}
            </span>
          )}
          <button
            type="button"
            className={BTN}
            onClick={() => {
              if (!confirmDiscard(dirty)) return
              setEditing(false)
              setDraft({})
              setError('')
            }}
          >
            Discard
          </button>
          <button type="button" className="border border-fg px-3 py-1 dash-control font-medium text-fg hover:bg-surface-hover disabled:opacity-50" disabled={dirty === 0 || busy} onClick={save}>
            {busy ? 'Saving' : 'Save profile'}
          </button>
        </div>
      )}
      {saved && !editing && (
        <p role="status" className="px-5 pt-3 dash-support text-fg-muted">
          Saved {savedFields}. Registry profile changes apply immediately.
        </p>
      )}
      <section aria-label="Profile" className="px-5 py-4">
        <h3 className="mb-2 dash-label font-medium text-fg-muted">Profile</h3>
        <dl>
          <Row label="Name">
            {editing ? (
              <input aria-label="Name" className={INPUT} maxLength={120} value={val('display_name', p.identity.display_name)} onChange={(e) => set({ display_name: e.target.value })} />
            ) : (
              p.identity.display_name || p.id
            )}
          </Row>
          <Row label="Description">
            {editing ? (
              <textarea
                aria-label="Description"
                className={INPUT}
                rows={3}
                maxLength={2000}
                value={val('description', p.identity.description)}
                onChange={(e) => set({ description: e.target.value })}
              />
            ) : (
              p.identity.description || <span className="text-fg-faint">none</span>
            )}
          </Row>
          <Row label="Status">
            {editing ? (
              <select aria-label="Status" className={INPUT} value={val('status', p.identity.status as ProjectStatus)} onChange={(e) => set({ status: e.target.value as ProjectStatus })}>
                {Object.entries(STATUS).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </select>
            ) : (
              (STATUS[p.identity.status] ?? p.identity.status)
            )}
          </Row>
          <Row label="Tags">
            {editing ? (
              <input aria-label="Tags" className={INPUT} placeholder="comma separated" value={val('tags', p.identity.tags).join(', ')} onChange={(e) => set({ tags: tags(e.target.value) })} />
            ) : p.identity.tags.length ? (
              p.identity.tags.join(', ')
            ) : (
              <span className="text-fg-faint">none</span>
            )}
          </Row>
        </dl>
      </section>
      <section aria-label="Guidance" className="border-t border-border-subtle px-5 py-4">
        <h3 className="mb-2 dash-label font-medium text-fg-muted">Guidance</h3>
        <dl>
          <Row label="Entry points">
            {editing ? (
              <textarea
                aria-label="Entry points"
                className={INPUT}
                rows={3}
                placeholder="one per line"
                value={val('entry_points', p.guidance.entry_points).join('\n')}
                onChange={(e) => set({ entry_points: lines(e.target.value) })}
              />
            ) : p.guidance.entry_points.length ? (
              <ul>
                {p.guidance.entry_points.map((e) => (
                  <li key={e} className="font-mono dash-support">
                    {e}
                  </li>
                ))}
              </ul>
            ) : (
              <span className="text-fg-faint">none</span>
            )}
          </Row>
          <Row label="Guidelines">
            {editing ? (
              <textarea aria-label="Guidelines" className={INPUT} rows={6} maxLength={8000} value={val('guidelines', p.guidance.guidelines)} onChange={(e) => set({ guidelines: e.target.value })} />
            ) : p.guidance.guidelines ? (
              <p className="whitespace-pre-wrap">{p.guidance.guidelines}</p>
            ) : (
              <span className="text-fg-faint">none</span>
            )}
          </Row>
        </dl>
      </section>
      <section aria-label="Catalog link" className="border-t border-border-subtle px-5 py-4">
        <h3 className="mb-2 dash-label font-medium text-fg-muted">Catalog</h3>
        {p.link.state === 'unlinked' ? (
          <p className="dash-body text-fg-secondary">Not linked to a catalog project{p.link.reason ? `: ${p.link.reason}` : '.'}</p>
        ) : (
          <dl>
            <Row label="Catalog project">{p.link.catalog_id ?? NOT_REPORTED}</Row>
            <Row label="Repository root">{p.catalog?.repo_root ?? NOT_REPORTED}</Row>
            <Row label="Tool servers">{p.catalog?.mcp_servers == null ? 'Inherited' : p.catalog.mcp_servers.length ? p.catalog.mcp_servers.join(', ') : 'none granted'}</Row>
          </dl>
        )}
      </section>
      <section aria-label="Used by" className="border-t border-border-subtle px-5 py-4">
        <h3 className="mb-2 dash-label font-medium text-fg-muted">Used by</h3>
        <dl>
          <Row label="Agents">{c?.agents == null ? NOT_REPORTED : c.agents.length ? c.agents.map((a) => a.name || a.id).join(', ') : 'none'}</Row>
          <Row label="Launches">{c?.launches == null ? NOT_REPORTED : c.launches.length ? c.launches.map((l) => l.id).join(', ') : 'none'}</Row>
          <Row label="Planner items">{c?.planner_items == null ? NOT_REPORTED : `${c.planner_items.open} open of ${c.planner_items.total}`}</Row>
          <Row label="Sessions">{c?.tether_sessions == null ? NOT_REPORTED : `${c.tether_sessions.live} live of ${c.tether_sessions.total}`}</Row>
        </dl>
      </section>
      {(p.soon ?? []).length > 0 && <p className="border-t border-border-subtle px-5 py-4 dash-support text-fg-faint">Not built yet: {p.soon?.join(', ')}.</p>}
    </div>
  )
}

/** Registry projects: list and profile. Profile edits write directly (operator session); catalog tool servers go through Builder changes. */
export function RegistryProjectsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q), 250)
    return () => clearTimeout(t)
  }, [q])
  const [load, retry] = useLoad(() => registryApi.list(debounced), [debounced])
  const rows: ProjectRow[] = load.status === 'ready' ? load.data.projects : []
  useEffect(() => {
    if (!id && rows.length > 0 && window.matchMedia?.('(min-width: 768px)').matches) navigate(`/registry/projects/${encodeURIComponent(rows[0].id)}`, { replace: true })
  }, [id, rows, navigate])
  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className="flex flex-wrap items-baseline gap-x-4 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold text-fg">Registry</h1>
        <p className="dash-support text-fg-muted">Projects and the profile agents read. Machines, services and repos are not built yet.</p>
      </header>
      <div className="flex min-h-0 flex-1">
        <div className={`${id ? 'hidden md:flex' : 'flex'} w-full shrink-0 flex-col border-r border-border md:w-80`}>
          <div className="border-b border-border p-3">
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search projects" aria-label="Search projects" className={INPUT} />
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
                <button type="button" onClick={retry} className={BTN}>
                  Retry
                </button>
              </div>
            )}
            {load.status === 'ready' && rows.length === 0 && <p className="p-4 dash-body text-fg-faint">No projects match.</p>}
            <ul aria-label="Projects" className="divide-y divide-border-subtle">
              {rows.map((r) => (
                <li key={r.id}>
                  <button
                    type="button"
                    aria-current={r.id === id ? 'true' : undefined}
                    onClick={() => navigate(`/registry/projects/${encodeURIComponent(r.id)}`)}
                    className={`block w-full px-4 py-2 text-left hover:bg-surface-hover ${r.id === id ? 'bg-surface' : ''}`}
                  >
                    <span className="block truncate dash-body text-fg">{r.display_name || r.id}</span>
                    <span className="block truncate dash-support text-fg-muted">
                      {STATUS[r.status] ?? r.status} · {r.link.state === 'linked' ? 'Linked' : 'Not linked'} · {count(r.counts?.agents)} agents
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        </div>
        <div className={`${id ? 'flex' : 'hidden md:flex'} min-w-0 flex-1 flex-col`}>
          {id ? <ProfileDetail id={decodeURIComponent(id)} onBack={() => navigate('/registry/projects')} /> : <p className="p-5 dash-body text-fg-faint">Select a project.</p>}
        </div>
      </div>
    </div>
  )
}
