import { useEffect, useState } from 'react'
import { ago } from '@console/lib/agent-activity'
import type { ForgeClient } from '../client'
import { prKey, type AuditEntry, type PrRef } from '../types'
import { LABEL } from './Comments'

const OP: Record<string, string> = { merge: 'Merged', review: 'Submitted a review', assign: 'Assigned an agent', comment: 'Commented', assignees: 'Changed assignees' }

/** Forge writes recorded for this pull request (GET /audit, operator-gated). Hidden when unreadable or empty; never shows content. */
export function AuditList({ client, prRef, now }: { client: ForgeClient; prRef: PrRef; now: number }) {
  const [rows, setRows] = useState<AuditEntry[]>([])
  const key = prKey(prRef)
  useEffect(() => {
    if (!client.listAudit) return
    let live = true
    client.listAudit(200).then((all) => { if (live) setRows(all.filter((r) => r.prKey === key).slice(0, 20)) }, () => undefined)
    return () => { live = false }
  }, [client, key])
  if (rows.length === 0) return null
  return (
    <section className="mt-8" aria-label="Forge activity">
      <h2 className={`${LABEL} mb-2`}>Forge activity · {rows.length}</h2>
      <ul className="divide-y divide-border text-xs text-fg-muted">
        {rows.map((r) => (
          <li key={r.id} className="flex flex-wrap items-baseline gap-x-3 py-2">
            <span className="text-fg">{OP[r.op] ?? r.op}</span>
            <span className={r.outcome === 'ok' || r.outcome === 'success' ? '' : 'text-danger'}>{r.outcome}{r.errorCode ? ` · ${r.errorCode}` : ''}</span>
            <span className="font-mono">{r.actor}</span>
            <span className="ml-auto">{ago(r.at, now)}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}
