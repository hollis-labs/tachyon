import { useEffect, useState } from 'react'
import { EmptyState } from '@hollis-labs/design-components'
import { useAgentsClient } from './context'
import type { AuditEntry } from './plan'
import { Chip, RiskBadge } from './ui'
import { clock } from './ui-util'

const KIND_TONE: Record<AuditEntry['kind'], string> = {
  plan: 'border-border text-fg-muted',
  approve: 'border-info/50 text-info',
  apply: 'border-success/50 text-success',
  apply_failed: 'border-danger/60 text-danger',
  rollback: 'border-warning/50 text-warning',
}

/** Per-agent change history. `version` bumps to reload after an apply. */
export function AuditTrail({ agentId, version = 0 }: { agentId: string; version?: number }) {
  const client = useAgentsClient()
  const [rows, setRows] = useState<AuditEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    client.listAudit(agentId).then((r) => active && (setRows(r), setError(null))).catch((e) => active && setError(e instanceof Error ? e.message : 'Audit unavailable'))
    return () => {
      active = false
    }
  }, [client, agentId, version])
  if (error) return <div className="p-4"><EmptyState variant="error" title="Audit trail unavailable" description={error} /></div>
  if (rows === null) return <div className="p-4 font-mono dash-support text-fg-faint">Loading audit trail…</div>
  if (rows.length === 0) return <div className="p-4"><EmptyState variant="empty" title="No changes recorded" description="Plans, approvals and applies for this agent appear here." /></div>
  return (
    <ol aria-label="Audit trail">
      {rows.map((e) => (
        <li key={e.id} className="border-b border-border/80 px-5 py-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className={`border px-1.5 py-0.5 font-mono dash-caption uppercase tracking-widest ${KIND_TONE[e.kind]}`}>{e.kind.replace('_', ' ')}</span>
            <span className="dash-body text-fg">{e.summary}</span>
            <RiskBadge risk={e.risk} />
            <span className="ml-auto font-mono dash-label text-fg-faint">{clock(e.at)}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono dash-label text-fg-faint">
            <span>by {e.actor.label ?? e.actor.id}</span>
            <span>{e.planId}</span>
            {e.revisionAfter && <span>{e.revisionBefore || 'unknown'} → {e.revisionAfter}</span>}
            {e.confirmation && <span className="flex flex-wrap gap-1">{e.confirmation.scope.map((s) => <Chip key={s}>{s}</Chip>)}</span>}
          </div>
        </li>
      ))}
    </ol>
  )
}
