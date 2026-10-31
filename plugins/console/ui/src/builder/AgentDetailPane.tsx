import { useDiscardGuard } from './DiscardGuard'
import { type ReactNode, useCallback, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Button, Checkbox, Input, OverflowMenu, Switch, Textarea } from '@hollis-labs/design-components'
import { Chip } from '../components/chip'
import { ChipListField } from './ChipListField'
import { DirtyBar } from './DirtyBar'
import { AGENT_FIELDS, EntityDialog } from './EntityDialog'
import { type AgentDetail, builderApi, type Change, errorMessage } from './api'
import { buildOps, changedPaths, type Draft, fieldByPath, newRequestId, reviewRows } from './changes'
import { OutlineNav } from './OutlineNav'
import { ChecksCard, HistorySection, ResolvedCard, UsedByCard, UsedBySection } from './InsightSections'
import { Cap, Cite, Field as Row, Tile } from './parts'
import { useChangeMode } from './capabilities'
import { ModeNote } from './ModeNote'
import { ReviewDialog } from './ReviewDialog'
import { useBuilderStream } from './stream'
import { useLoad } from './useLoad'
import { INHERITS, NOT_REPORTED, SECTIONS, STATE_LABEL } from './vocab'

/** null means the backend did not report it; an empty string is a real, empty value. */
const text = (v: string | null | undefined) => (v == null ? NOT_REPORTED : v === '' ? <span className="text-fg-faint">none</span> : v)

const chipsOf = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])

interface EditCtx {
  agent: AgentDetail
  editing: boolean
  canEdit: boolean
  draft: Draft
  set: (path: string, value: unknown) => void
}

/** A row that shows the value, or an editor for it while editing. Chip lists edit in place. */
function Field({ path, ctx, children }: { path: string; ctx: EditCtx; children?: ReactNode }) {
  const def = fieldByPath(path)
  if (!def) return <Row label={path}>{children}</Row>
  const value = path in ctx.draft ? ctx.draft[path] : def.read(ctx.agent)
  if (def.kind === 'chips') {
    return (
      <Row label={def.label}>
        <ChipListField label={def.label} value={chipsOf(value)} editable={ctx.canEdit} onChange={(next) => ctx.set(path, next)} />
      </Row>
    )
  }
  if (!ctx.editing) return <Row label={def.label}>{children}</Row>
  const id = `f-${path}`
  let editor: ReactNode
  if (def.kind === 'bool') {
    editor = <Checkbox id={id} checked={value === true} onCheckedChange={(v) => ctx.set(path, v === true)} />
  } else if (def.kind === 'longtext') {
    editor = <Textarea id={id} rows={3} value={String(value)} onChange={(e) => ctx.set(path, e.target.value)} />
  } else {
    editor = <Input id={id} value={String(value)} onChange={(e) => ctx.set(path, e.target.value)} />
  }
  return (
    <div className="grid gap-0.5 py-[3px] sm:grid-cols-[130px_1fr] sm:gap-3">
      <label htmlFor={id} className="font-mono text-caption uppercase tracking-label text-fg-muted sm:pt-[7px]">
        {def.label}
      </label>
      <div className="min-w-0">{editor}</div>
    </div>
  )
}

function Section({ id, children }: { id: string; children: ReactNode }) {
  const label = SECTIONS.find((s) => s.id === id)?.label ?? id
  return (
    <section id={`section-${id}`} aria-labelledby={`h-${id}`} className="flex scroll-mt-4 flex-col gap-2">
      <Cap id={`h-${id}`}>{label}</Cap>
      <dl className="flex flex-col gap-2">{children}</dl>
    </section>
  )
}

/** Launch profiles of this agent. New launch arrives with the Launches slice. */
function LaunchProfiles({ launches }: { launches: NonNullable<AgentDetail['launches']> }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-panel border border-border px-3 py-2 sm:ml-[142px]">
      <div className="flex items-center justify-between gap-2">
        <Cap>Launch profiles</Cap>
        <button type="button" disabled title="Creating a launch arrives with the Launches slice" className="inline-flex h-5 items-center rounded-full border border-dashed border-border px-2 text-label text-fg-muted disabled:opacity-60">
          + New launch
        </button>
      </div>
      {launches.length === 0 && <p className="text-xs text-fg-faint">No launch profiles.</p>}
      {launches.map((l) => (
        <div key={l.id} className="flex flex-wrap items-center gap-2.5 text-xs">
          <i aria-hidden className={`size-[7px] shrink-0 rounded-full ${l.enabled ? 'bg-fg opacity-90' : 'bg-fg-faint opacity-60'}`} />
          <b className="whitespace-nowrap font-mono">{l.id}</b>
          <span className="text-fg-muted">{l.project} · {l.worker}</span>
          <span className="flex-1" />
          {l.live_count > 0 && <Chip>{l.live_count} live</Chip>}
          <Link to="/builder/launches" className="whitespace-nowrap text-label text-fg-secondary hover:text-fg">
            Launches ▸
          </Link>
        </div>
      ))}
    </div>
  )
}

/** Editor model of one agent: read-only until Edit; saving goes through Review and host approval. */
export function AgentDetailPane({ id, onBack }: { id: string; onBack: () => void }) {
  const [load, retry] = useLoad(() => builderApi.agent(id), [id])
  const [usedBy, retryUsedBy] = useLoad(() => builderApi.usedBy(id), [id])
  const [elsewhere, setElsewhere] = useState(false)
  const { guard, dialog: discardDialog } = useDiscardGuard()
  const [mode, recheckMode] = useChangeMode()
  const [prepared, setPrepared] = useState<string>()
  const filePath = load.status === 'ready' ? load.data.revision?.path : null
  useBuilderStream((e) => {
    if (e.type === 'catalog' && e.source === 'observed' && filePath && e.changed_paths?.some((p) => filePath.endsWith(p) || p.endsWith(filePath))) setElsewhere(true)
  })
  const scroller = useRef<HTMLDivElement>(null)
  const [editing, setEditing] = useState(false)
  const [duplicating, setDuplicating] = useState(false)
  const [draft, setDraft] = useState<Draft>({})
  const [review, setReview] = useState<Change | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const applied = useCallback(() => {
    setEditing(false)
    setDraft({})
    setPrepared(undefined)
    retry()
  }, [retry])

  if (load.status === 'loading')
    return (
      <div aria-busy="true" className="space-y-3 p-5">
        <div className="h-8 w-1/2 animate-pulse bg-surface" />
        <div className="h-40 animate-pulse bg-surface" />
      </div>
    )
  if (load.status === 'error')
    return (
      <div role="alert" className="space-y-2 p-5">
        <p className="dash-body text-fg-secondary">{load.message}</p>
        <Button type="button" onClick={retry} variant="outline" size="sm">
          Retry
        </Button>
      </div>
    )
  const a: AgentDetail = load.data
  const dirty = changedPaths(a, draft)
  const canEdit = a.editable && mode !== 'unavailable'
  const ctx: EditCtx = {
    agent: a,
    editing,
    canEdit,
    draft,
    set: (path, value) => {
      if (canEdit) setEditing(true)
      setDraft((d) => ({ ...d, [path]: value }))
    },
  }
  const enabled = 'runtime.enabled' in draft ? draft['runtime.enabled'] === true : a.runtime.enabled !== false
  const projects = [...new Set((a.launches ?? []).map((l) => l.project).filter(Boolean))]
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
    setBusy(true)
    setError('')
    try {
      setReview(await builderApi.createChange({ request_id: newRequestId(), ops: buildOps(a, draft) }))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border-subtle px-4 py-3">
        <Button type="button" variant="outline" size="sm" onClick={onBack} className="sm:hidden">
          ‹ Agents
        </Button>
        <Tile name={a.identity.name} large />
        <div className="flex min-w-0 basis-full flex-col sm:flex-1 sm:basis-0">
          <h2 className="truncate font-sans text-base font-semibold normal-case tracking-normal text-fg">{a.identity.name}</h2>
          <p className="truncate font-mono text-label text-fg-muted">
            {a.identity.id}
            {a.identity.urn ? ` · ${a.identity.urn}` : ''}
          </p>
        </div>
        {!a.editable && <Chip>read-only</Chip>}
        {a.state === 'retired' && <Chip>retired</Chip>}
        <label className="inline-flex items-center gap-2 text-xs text-fg-secondary">
          <Switch size="sm" checked={enabled} disabled={!canEdit} onCheckedChange={(v) => ctx.set('runtime.enabled', v === true)} aria-label="Enabled" />
          Enabled
        </label>
        {canEdit && !editing && (
          <Button type="button" onClick={() => setEditing(true)} variant="outline" size="sm">
            Edit
          </Button>
        )}
        <OverflowMenu
          ariaLabel="More actions"
          actions={[
            { label: 'Copy slug', onSelect: () => void navigator.clipboard?.writeText(a.identity.id) },
            ...(mode !== 'unavailable' ? [{ label: 'Duplicate agent', onSelect: () => setDuplicating(true) }] : []),
            ...(a.identity.urn ? [{ label: 'Copy URN', onSelect: () => void navigator.clipboard?.writeText(a.identity.urn ?? '') }] : []),
          ]}
        />
      </header>
      {mode === 'unavailable' && <ModeNote onCheck={recheckMode} />}
      {elsewhere && (
        <div role="status" className="flex flex-wrap items-center gap-3 px-5 py-2 dash-support text-fg-muted">
          <span className="mr-auto">This agent changed elsewhere.{dirty.length > 0 ? ' Reloading drops your unsaved changes.' : ''}</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => guard(dirty.length, () => {
              setEditing(false)
              setDraft({})
              setElsewhere(false)
              retry()
            })}
          >
            Reload
          </Button>
        </div>
      )}
      {discardDialog}
      {duplicating && (
        <EntityDialog
          kind="agent"
          mode="create"
          noun="agent"
          fields={AGENT_FIELDS}
          initial={{ 'identity.name': `${a.identity.name} copy`, 'identity.description': a.identity.description ?? '', 'runtime.provider': a.runtime.provider ?? '', 'runtime.model': a.runtime.model ?? '' }}
          onClose={() => setDuplicating(false)}
          onApplied={() => setDuplicating(false)}
        />
      )}
      {review && (
        <ReviewDialog
          initial={review}
          subject={a.identity.name}
          rows={reviewRows(a, draft)}
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
      <div className="flex min-h-0 flex-1">
        <OutlineNav root={scroller} />
        <div ref={scroller} className="flex min-w-0 flex-1 flex-col gap-[22px] overflow-auto px-5 py-4 [&>*]:flex-none">
          <Section id="identity">
            <Row label="Name">{a.identity.name}</Row>
            <Row label="Slug">
              <span className="font-mono">{a.identity.id}</span>
            </Row>
            <Field path="identity.roles" ctx={ctx} />
            <Field path="identity.description" ctx={ctx}>
              {text(a.identity.description)}
            </Field>
          </Section>
          <Section id="runtime">
            <Row label="Provider">{text(a.runtime.provider)}</Row>
            <Field path="runtime.model" ctx={ctx}>
              {text(a.runtime.model)}
            </Field>
            {a.runtime.extends && <Row label="Extends">{a.runtime.extends}</Row>}
            <Row label="Project">
              {projects.length ? (
                projects.map((p) => (
                  <Cite key={p} to={`/registry/projects/${encodeURIComponent(p)}`}>
                    {p}
                  </Cite>
                ))
              ) : (
                <span className="text-fg-faint">none</span>
              )}
            </Row>
          </Section>
          <LaunchProfiles launches={a.launches ?? []} />
          <Section id="prompt">
            <Row label="Boot profile">
              {a.prompt?.boot_fragments?.length ? a.prompt.boot_fragments.map((b) => <Cite key={b} to={`/builder/library/prompts/${encodeURIComponent(b)}`}>{b}</Cite>) : <span className="text-fg-faint">none</span>}
            </Row>
            <Row label="System prompt">
              {a.prompt?.prompts?.length ? a.prompt.prompts.map((p) => <Cite key={p} to={`/builder/library/prompts/${encodeURIComponent(p)}`}>{p}</Cite>) : <span className="text-fg-faint">none</span>}
              {a.prompt?.parts?.length ? <span className="text-xs text-fg-muted">{a.prompt.parts.map((p) => p.label).join(', ')}</span> : null}
            </Row>
          </Section>
          <Section id="tools">
            <Row label="Tool servers">
              {a.tools_skills?.mcp_servers == null ? <span className="text-fg-faint">{INHERITS}</span> : <ChipListField label="Tool servers" value={a.tools_skills.mcp_servers} empty="none granted" />}
            </Row>
            <Field path="tools_skills.skills" ctx={ctx} />
          </Section>
          <Section id="policy">
            <Field path="policy.permission_mode" ctx={ctx}>
              {text(a.policy?.permission_mode)}
            </Field>
            <Field path="policy.default_sandbox" ctx={ctx}>
              {text(a.policy?.default_sandbox)}
            </Field>
            <Field path="policy.network" ctx={ctx}>
              {text(a.policy?.network)}
            </Field>
            <Field path="policy.mcp_scopes" ctx={ctx} />
            <Field path="policy.rules" ctx={ctx} />
          </Section>
          <UsedBySection load={usedBy} retry={retryUsedBy} />
          <HistorySection id={a.id} />
          <div className="flex flex-col gap-3.5 xl:hidden">
            <ResolvedCard id={a.id} />
            <ChecksCard id={a.id} />
          </div>
        </div>
        <aside aria-label="Resolved, used by and checks" className="hidden w-[300px] shrink-0 flex-col gap-3.5 overflow-auto border-l border-border-subtle p-3.5 xl:flex">
          <ResolvedCard id={a.id} />
          <UsedByCard load={usedBy} retry={retryUsedBy} />
          <ChecksCard id={a.id} />
        </aside>
      </div>
      {editing && (
        <DirtyBar
          labels={dirty.map((p) => fieldByPath(p)?.label)}
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
    </div>
  )
}
