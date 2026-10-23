// Shared HITL card building blocks. All strings are untrusted: text only; links
// are rendered as anchors only for http(s) URLs.
import { Button } from '@hollis-labs/design-components'
import type { ReactNode } from 'react'
import type { ConsoleRef } from '../events/types'
import type { CardActionController } from '../cards/types'
import { fmtRemaining } from './model'
import type { HitlCardState } from './use-hitl-card'
import type { HitlAuditEntry, HitlRequest, HitlRisk } from './types'

const RISK: Record<HitlRisk, string> = {
  low: 'border-border text-fg-secondary',
  medium: 'border-info/60 text-info',
  high: 'border-warning/70 text-warning',
  critical: 'border-danger/70 text-danger',
}

export function RiskBadge({ risk }: { risk: HitlRisk }) {
  return <span className={`border px-1.5 py-px font-mono text-micro uppercase tracking-label ${RISK[risk]}`}>{risk} risk</span>
}

export function Countdown({ remaining }: { remaining: number | undefined }) {
  if (remaining === undefined) return null
  const urgent = remaining > 0 && remaining < 300_000
  return (
    <span data-hitl-countdown className={`font-mono text-label ${remaining <= 0 ? 'text-danger' : urgent ? 'text-warning' : 'text-fg-secondary'}`}>
      {remaining <= 0 ? 'expired' : `expires in ${fmtRemaining(remaining)}`}
    </span>
  )
}

function safeHref(u: string | undefined): string | undefined {
  return u && /^https?:\/\//i.test(u) ? u : undefined
}

export function ContextLinks({ links }: { links: ConsoleRef[] }) {
  if (links.length === 0) return null
  return (
    <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1">
      <span className="font-mono text-micro uppercase tracking-label text-fg-muted">context</span>
      {links.map((l) => {
        const href = safeHref(l.url)
        const text = `${l.type}: ${l.label ?? l.id}`
        return href ? (
          <a key={`${l.type}:${l.id}`} href={href} target="_blank" rel="noreferrer noopener" className="font-mono text-label text-info underline-offset-2 hover:underline">{text}</a>
        ) : (
          <span key={`${l.type}:${l.id}`} className="font-mono text-label text-fg-secondary">{text}</span>
        )
      })}
    </div>
  )
}

export function MetaLine({ req, remaining }: { req: HitlRequest; remaining: number | undefined }) {
  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
      <RiskBadge risk={req.risk} />
      <span className="font-mono text-label text-fg-secondary">from <span className="text-fg">{req.requester.label ?? req.requester.id}</span></span>
      <Countdown remaining={req.status === 'pending' ? remaining : undefined} />
    </div>
  )
}

const fmtTime = (t: number) => new Date(t).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })

export function AuditTrail({ audit }: { audit: HitlAuditEntry[] }) {
  if (audit.length === 0) return null
  return (
    <details className="mt-3 border-t border-border-subtle pt-2">
      <summary className="cursor-pointer font-mono text-micro uppercase tracking-label text-fg-muted hover:text-fg-secondary">Decision audit ({audit.length})</summary>
      <ol className="mt-1.5 space-y-0.5">
        {audit.map((a, i) => (
          <li key={i} className="grid grid-cols-[8.5rem_1fr] gap-x-3 font-mono text-label">
            <time className="text-fg-muted">{fmtTime(a.at)}</time>
            <span className="text-fg-secondary">{a.actor} <span className="text-fg">{a.action}</span>{a.note ? <span className="text-fg-muted"> - {a.note}</span> : null}</span>
          </li>
        ))}
      </ol>
    </details>
  )
}

export function StatusLine({ controller, ids }: { controller: CardActionController; ids: string[] }) {
  const last = ids.map((i) => controller.status[i]).find((s) => s && !s.pending && s.message)
  if (!last?.message) return null
  return <span role={last.ok ? 'status' : 'alert'} className={`font-mono text-label ${last.ok ? 'text-success' : 'text-danger'}`}>{last.message}</span>
}

export function ClosedNote({ s }: { s: HitlCardState }) {
  const { req, expiredLocally } = s
  if (req.status === 'answered') return <p className="mt-2 font-mono text-label text-success">{req.outcome?.label ?? 'Answered'}{req.outcome?.by ? ` by ${req.outcome.by}` : ''}</p>
  if (req.status === 'withdrawn') return <p className="mt-2 font-mono text-label text-fg-secondary">Withdrawn by the requester</p>
  if (req.status === 'expired' || expiredLocally) return <p className="mt-2 font-mono text-label text-danger">Expired - ask the requester to send it again</p>
  return null
}

export function Btn({ intent = 'neutral', children, ...p }: { intent?: 'primary' | 'neutral' | 'danger'; children: ReactNode } & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const variant = { primary: 'default', neutral: 'outline', danger: 'destructive' }[intent] as 'default' | 'outline' | 'destructive'
  return (
    <Button type="button" variant={variant} size="xs" {...p} className="font-mono uppercase tracking-label">{children}</Button>
  )
}

export const FIELD = 'w-full border border-border bg-bg-elevated px-2 py-1.5 text-sm text-fg placeholder:text-fg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50'
