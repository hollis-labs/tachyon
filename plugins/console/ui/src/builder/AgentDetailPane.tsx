import { type ReactNode, useCallback, useRef, useState } from 'react'
import { Button, Checkbox, Input, Textarea } from '@hollis-labs/design-components'
import { type AgentDetail, builderApi, type Change, errorMessage } from './api'
import { buildOps, changedPaths, changingText, confirmDiscard, type Draft, fieldByPath, newRequestId } from './changes'
import { OutlineNav } from './OutlineNav'
import { ChecksSection, HistorySection, ResolvedSection, UsedBySection } from './InsightSections'
import { ReviewDialog } from './ReviewDialog'
import { useBuilderStream } from './stream'
import { useLoad } from './useLoad'
import { INHERITS, NOT_REPORTED, SECTIONS, STATE_LABEL } from './vocab'

const CHIP = 'inline-flex items-center border border-border px-1.5 py-px font-mono text-caption text-fg-secondary'

const text = (v: string | null | undefined) => (v ? v : NOT_REPORTED)

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[10rem_1fr] gap-3 border-b border-border-subtle py-2">
      <dt className="dash-support text-fg-muted">{label}</dt>
      <dd className="min-w-0 dash-body text-fg [overflow-wrap:anywhere]">{children}</dd>
    </div>
  )
}

function Chips({ items, empty = 'none' }: { items?: string[] | null; empty?: string }) {
  if (!items || items.length === 0) return <span className="text-fg-faint">{empty}</span>
  return (
    <ul className="flex flex-wrap gap-1.5">
      {items.map((i) => (
        <li key={i} className={CHIP}>
          {i}
        </li>
      ))}
    </ul>
  )
}

interface EditCtx {
  agent: AgentDetail
  editing: boolean
  draft: Draft
  set: (path: string, value: unknown) => void
}

/** A row that shows the value, or an editor for it while editing. */
function Field({ path, ctx, children }: { path: string; ctx: EditCtx; children: ReactNode }) {
  const def = fieldByPath(path)
  if (!def || !ctx.editing) return <Row label={def?.label ?? path}>{children}</Row>
  const value = path in ctx.draft ? ctx.draft[path] : def.read(ctx.agent)
  const id = `f-${path}`
  let editor: ReactNode
  if (def.kind === 'bool') {
    editor = <Checkbox id={id} checked={value === true} onCheckedChange={(v) => ctx.set(path, v === true)} />
  } else if (def.kind === 'chips') {
    editor = (
      <Input
        id={id}
        value={(value as string[]).join(', ')}
        placeholder="comma separated"
        onChange={(e) =>
          ctx.set(
            path,
            e.target.value
              .split(',')
              .map((x) => x.trim())
              .filter(Boolean),
          )
        }
      />
    )
  } else if (def.kind === 'longtext') {
    editor = <Textarea id={id} rows={3} value={String(value)} onChange={(e) => ctx.set(path, e.target.value)} />
  } else {
    editor = <Input id={id} value={String(value)} onChange={(e) => ctx.set(path, e.target.value)} />
  }
  return (
    <div className="grid grid-cols-[10rem_1fr] gap-3 border-b border-border-subtle py-2">
      <label htmlFor={id} className="dash-support text-fg-muted">
        {def.label}
      </label>
      <div className="min-w-0">{editor}</div>
    </div>
  )
}

function Section({ id, children }: { id: string; children: ReactNode }) {
  const label = SECTIONS.find((s) => s.id === id)?.label ?? id
  return (
    <section id={`section-${id}`} aria-labelledby={`h-${id}`} className="scroll-mt-4 px-5 py-4">
      <h3 id={`h-${id}`} className="mb-2 font-mono dash-label uppercase tracking-widest text-fg-faint">
        {label}
      </h3>
      <dl>{children}</dl>
    </section>
  )
}

/** Editor model of one agent: read-only until Edit; saving goes through Review and host approval. */
export function AgentDetailPane({ id, onBack }: { id: string; onBack: () => void }) {
  const [load, retry] = useLoad(() => builderApi.agent(id), [id])
  const [elsewhere, setElsewhere] = useState(false)
  const filePath = load.status === 'ready' ? load.data.revision?.path : null
  useBuilderStream((e) => {
    if (e.type === 'catalog' && e.source === 'observed' && filePath && e.changed_paths?.some((p) => filePath.endsWith(p) || p.endsWith(filePath))) setElsewhere(true)
  })
  const scroller = useRef<HTMLDivElement>(null)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Draft>({})
  const [review, setReview] = useState<Change | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const applied = useCallback(() => {
    setEditing(false)
    setDraft({})
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
  const ctx: EditCtx = {
    agent: a,
    editing,
    draft,
    set: (path, value) => setDraft((d) => ({ ...d, [path]: value })),
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
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
        <button type="button" onClick={onBack} className="dash-control text-fg-muted hover:text-fg md:hidden">
          ← Agents
        </button>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-lg font-semibold text-fg">{a.identity.name}</h2>
          <p className="truncate font-mono dash-support text-fg-muted">
            {a.identity.id}
            {a.identity.urn ? ` · ${a.identity.urn}` : ''}
          </p>
        </div>
        <span className={CHIP}>{STATE_LABEL[a.state]}</span>
        {!a.editable && <span className={CHIP}>Read-only</span>}
        {a.editable && !editing && (
          <Button type="button" onClick={() => setEditing(true)} variant="outline" size="sm">
            Edit
          </Button>
        )}
      </header>
      {elsewhere && (
        <div role="status" className="flex flex-wrap items-center gap-3 px-5 py-2 dash-support text-fg-muted">
          <span className="mr-auto">This agent changed elsewhere.{dirty.length > 0 ? ' Reloading drops your unsaved changes.' : ''}</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              if (!confirmDiscard(dirty.length)) return
              setEditing(false)
              setDraft({})
              setElsewhere(false)
              retry()
            }}
          >
            Reload
          </Button>
        </div>
      )}
      {editing && (
        <div role="status" className="flex flex-wrap items-center gap-3 border-b border-border bg-surface px-5 py-2 dash-support text-fg-secondary">
          <span className="mr-auto">{changingText(dirty.map((p) => fieldByPath(p)?.label))}</span>
          {error && (
            <span role="alert" className="text-danger">
              {error}
            </span>
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              if (!confirmDiscard(dirty.length)) return
              setEditing(false)
              setDraft({})
              setError('')
            }}
          >
            Discard
          </Button>
          <Button type="button" disabled={dirty.length === 0 || busy} variant="default" size="sm" onClick={openReview}>
            {busy ? 'Checking' : 'Review changes'}
          </Button>
        </div>
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
      <div className="flex min-h-0 flex-1">
        <OutlineNav root={scroller} />
        <div ref={scroller} className="min-w-0 flex-1 divide-y divide-border-subtle overflow-auto">
          <Section id="identity">
            <Row label="Name">{a.identity.name}</Row>
            <Field path="identity.description" ctx={ctx}>
              {text(a.identity.description)}
            </Field>
            <Field path="identity.roles" ctx={ctx}>
              <Chips items={a.identity.roles} />
            </Field>
          </Section>
          <Section id="runtime">
            <Row label="Provider">{text(a.runtime.provider)}</Row>
            <Field path="runtime.model" ctx={ctx}>
              {text(a.runtime.model)}
            </Field>
            <Row label="Extends">{a.runtime.extends ?? 'none'}</Row>
            <Field path="runtime.enabled" ctx={ctx}>
              {a.runtime.enabled === false ? 'No' : 'Yes'}
            </Field>
          </Section>
          <Section id="prompt">
            <Row label="Prompts">
              <Chips items={a.prompt?.prompts} />
            </Row>
            <Row label="Boot fragments">
              <Chips items={a.prompt?.boot_fragments} />
            </Row>
            <Row label="Compiled parts">
              {a.prompt?.parts?.length ? (
                <ul className="space-y-1">
                  {a.prompt.parts.map((p) => (
                    <li key={`${p.kind}:${p.id}`}>
                      {p.label}{' '}
                      <span className="font-mono dash-support text-fg-muted">
                        rev {p.rev} · ~{p.tokens_est} tokens
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <span className="text-fg-faint">none</span>
              )}
            </Row>
          </Section>
          <Section id="tools">
            <Row label="Tool servers">
              {a.tools_skills?.mcp_servers == null ? <span className="text-fg-faint">{INHERITS}</span> : <Chips items={a.tools_skills.mcp_servers} empty="none granted" />}
            </Row>
            <Field path="tools_skills.skills" ctx={ctx}>
              <Chips items={a.tools_skills?.skills} />
            </Field>
          </Section>
          <Section id="policy">
            <Field path="policy.permission_mode" ctx={ctx}>
              {text(a.policy?.permission_mode)}
            </Field>
            <Field path="policy.network" ctx={ctx}>
              {text(a.policy?.network)}
            </Field>
            <Field path="policy.default_sandbox" ctx={ctx}>
              {text(a.policy?.default_sandbox)}
            </Field>
            <Field path="policy.mcp_scopes" ctx={ctx}>
              <Chips items={a.policy?.mcp_scopes} />
            </Field>
          </Section>
          <ResolvedSection id={a.id} />
          <UsedBySection id={a.id} />
          <ChecksSection id={a.id} />
          <HistorySection id={a.id} />
        </div>
      </div>
    </div>
  )
}
