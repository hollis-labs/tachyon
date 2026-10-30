import { type ReactNode, useRef } from 'react'
import { type AgentDetail, builderApi } from './api'
import { OutlineNav } from './OutlineNav'
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

/** Read-only editor model of one agent. Editing and Review arrive with the changes flow. */
export function AgentDetailPane({ id, onBack }: { id: string; onBack: () => void }) {
  const [load, retry] = useLoad(() => builderApi.agent(id), [id])
  const scroller = useRef<HTMLDivElement>(null)

  if (load.status === 'loading') return <div aria-busy="true" className="space-y-3 p-5"><div className="h-8 w-1/2 animate-pulse bg-surface" /><div className="h-40 animate-pulse bg-surface" /></div>
  if (load.status === 'error')
    return (
      <div role="alert" className="space-y-2 p-5">
        <p className="dash-body text-fg-secondary">{load.message}</p>
        <button type="button" onClick={retry} className="border border-border px-2 py-1 dash-control text-fg-secondary hover:bg-surface-hover">Retry</button>
      </div>
    )
  const a: AgentDetail = load.data
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
        <button type="button" onClick={onBack} className="dash-control text-fg-muted hover:text-fg md:hidden">← Agents</button>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-lg font-semibold text-fg">{a.identity.name}</h2>
          <p className="truncate font-mono dash-support text-fg-muted">{a.identity.id}{a.identity.urn ? ` · ${a.identity.urn}` : ''}</p>
        </div>
        <span className={CHIP}>{STATE_LABEL[a.state]}</span>
        {!a.editable && <span className={CHIP}>Read-only</span>}
      </header>
      <div className="flex min-h-0 flex-1">
        <OutlineNav root={scroller} />
        <div ref={scroller} className="min-w-0 flex-1 divide-y divide-border-subtle overflow-auto">
          <Section id="identity">
            <Row label="Name">{a.identity.name}</Row>
            <Row label="Description">{text(a.identity.description)}</Row>
            <Row label="Roles"><Chips items={a.identity.roles} /></Row>
          </Section>
          <Section id="runtime">
            <Row label="Provider">{text(a.runtime.provider)}</Row>
            <Row label="Model">{text(a.runtime.model)}</Row>
            <Row label="Extends">{a.runtime.extends ?? 'none'}</Row>
            <Row label="Enabled">{a.runtime.enabled === false ? 'No' : 'Yes'}</Row>
          </Section>
          <Section id="prompt">
            <Row label="Prompts"><Chips items={a.prompt?.prompts} /></Row>
            <Row label="Boot fragments"><Chips items={a.prompt?.boot_fragments} /></Row>
            <Row label="Compiled parts">
              {a.prompt?.parts?.length ? (
                <ul className="space-y-1">
                  {a.prompt.parts.map((p) => (
                    <li key={`${p.kind}:${p.id}`}>{p.label} <span className="font-mono dash-support text-fg-muted">rev {p.rev} · ~{p.tokens_est} tokens</span></li>
                  ))}
                </ul>
              ) : <span className="text-fg-faint">none</span>}
            </Row>
          </Section>
          <Section id="tools">
            <Row label="Tool servers">{a.tools_skills?.mcp_servers == null ? <span className="text-fg-faint">{INHERITS}</span> : <Chips items={a.tools_skills.mcp_servers} empty="none granted" />}</Row>
            <Row label="Skills"><Chips items={a.tools_skills?.skills} /></Row>
          </Section>
          <Section id="policy">
            <Row label="Permission mode">{text(a.policy?.permission_mode)}</Row>
            <Row label="Network">{text(a.policy?.network)}</Row>
            <Row label="Sandbox">{text(a.policy?.default_sandbox)}</Row>
            <Row label="Scopes"><Chips items={a.policy?.mcp_scopes} /></Row>
          </Section>
        </div>
      </div>
    </div>
  )
}
