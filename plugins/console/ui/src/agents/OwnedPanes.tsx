import { AdminSection as Section } from '@hollis-labs/kit-admin'
import { AgentPublication } from './AgentPublication'
import { Btn } from './ui'
import { isRetired } from './owned-builder-client'
import type { OwnedEditor } from './use-owned-editor'

export const ownershipLabel = (row: { ownership: string; deleted_at?: string; source?: { deleted_at?: string } }) =>
  row.deleted_at || row.ownership === 'source' && row.source?.deleted_at ? 'retired' : row.ownership === 'tachyon' ? 'owned by Tachyon' : 'provider observation'

/** Adopt, retire and restore. Publication to Tether stays a reviewed action on owned agents. */
export function OwnedLifecyclePane({ editor }: { editor: OwnedEditor }) {
  const { kind, base, dirty, busy, locked, lifecycle } = editor
  if (!base) return <p className="p-4 dash-body text-fg-muted">Select a saved {kind}.</p>
  return <article aria-label={`${kind} lifecycle`}>
    <Section title="Lifecycle">
      <p className="mb-3 dash-body text-fg-muted">{ownershipLabel(base)} · revision {base.revision}</p>
      {dirty && <p className="mb-3 dash-support text-fg-muted">Save or discard your edits before changing lifecycle.</p>}
      {locked && !isRetired(base) && <p className="mb-3 dash-body text-fg-muted">This provider observation is read-only. Adopt it to manage its definition in Tachyon.</p>}
      {isRetired(base) && <p className="mb-3 dash-body text-fg-muted">This record is retired. History is retained.</p>}
      <div className="flex flex-wrap gap-3">
        {!dirty && (base.ownership === 'tachyon'
          ? <Btn disabled={busy} onClick={() => void lifecycle(base.deleted_at ? 'restore' : 'delete')}>{base.deleted_at ? 'Restore' : 'Retire'} {kind}</Btn>
          : !isRetired(base) && <Btn disabled={busy} onClick={() => void lifecycle('take_ownership')}>Adopt definition into Tachyon</Btn>)}
      </div>
    </Section>
    {kind === 'agent' && base.ownership === 'tachyon' && <AgentPublication key={base.id} agent={base} disabled={dirty || busy || isRetired(base)} />}
  </article>
}

export function OwnedHistoryPane({ editor }: { editor: OwnedEditor }) {
  const { kind, base, history, busy, readHistory } = editor
  if (!base) return <p className="p-4 dash-body text-fg-muted">Select a saved {kind}.</p>
  return <article aria-label={`${kind} history`}>
    <Section title="Saved history">
      {history ? <ul>{history.items.map(item => <li key={item.record.revision} className="py-2 dash-support">Revision {item.record.revision} · {item.operation} · {item.actor} · {item.recorded_at}</li>)}</ul>
        : <p className="mb-3 dash-body text-fg-muted">History is read on demand.</p>}
      <div className="mt-3 flex gap-3">
        {!history && <Btn disabled={busy} onClick={() => void readHistory()}>Load history</Btn>}
        {history?.next_after_revision && <Btn disabled={busy} onClick={() => void readHistory()}>More history</Btn>}
      </div>
    </Section>
  </article>
}
