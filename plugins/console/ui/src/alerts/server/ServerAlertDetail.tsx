import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Pill } from '@hollis-labs/design-components'
import { X } from 'lucide-react'
import { ago } from '@console/lib/agent-activity'
import { Linkified } from '../../autolink'
import { approvalClient, type ApprovalClient } from '../../approvals/client'
import { useApprovals } from '../../approvals/context'
import { DecisionDetail } from '../../approvals/detail'
import { useRestoreFocus } from '../../shell/restore-focus'
import { AlertIcon } from '../AlertIcon'
import { Chip } from '../../components/chip'
import { AlertLinkButton } from '../AlertLinkButton'
import { useAlertsController } from '../context'
import { safeHref } from '../policy'
import { SEVERITY_STYLE } from '../severity-style'
import { createAlertsClient, type AlertsClient } from './client'
import type { ServerAlertDetail as Detail } from './types'

const H = 'font-mono dash-caption uppercase tracking-label text-fg-faint'
const SNOOZES = [
  { label: '15 minutes', ms: 15 * 60_000 },
  { label: '1 hour', ms: 3_600_000 },
  { label: '4 hours', ms: 4 * 3_600_000 },
]

/** ms until 09:00 tomorrow, local time. */
function untilTomorrow(now: number) {
  const d = new Date(now)
  d.setDate(d.getDate() + 1)
  d.setHours(9, 0, 0, 0)
  return d.getTime() - now
}

/**
 * Detail drawer for a server alert: facts from GET /alerts/{id}, and for an approval alert the shared
 * Approvals decision component, so Approve and Deny are the same control, rules and audit as in the
 * Approvals page. There is no decision endpoint on the alert itself.
 */
export function ServerAlertDetailModal({ client: clientProp, approvals: approvalsProp }: { client?: AlertsClient; approvals?: ApprovalClient }) {
  const c = useAlertsController()
  const entry = c.expandedId ? c.entries.find((e) => e.alert.id === c.expandedId) : undefined
  if (!entry) return null
  return <Drawer key={entry.alert.id} id={entry.alert.id} client={clientProp} approvals={approvalsProp} />
}

function Drawer({ id, client: clientProp, approvals: approvalsProp }: { id: string; client?: AlertsClient; approvals?: ApprovalClient }) {
  const c = useAlertsController()
  const queue = useApprovals()
  const approvals = approvalsProp ?? queue?.client ?? approvalClient
  const [client] = useState(() => clientProp ?? createAlertsClient())
  const [detail, setDetail] = useState<Detail | null>(null)
  const [failed, setFailed] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const entry = c.entries.find((e) => e.alert.id === id)
  const onClose = () => c.expand(null)

  const load = useCallback(() => {
    client.detail(id).then((d) => { setDetail(d); setFailed(false) }).catch(() => setFailed(true))
  }, [client, id])
  useEffect(load, [load])
  useRestoreFocus(true, '[data-alert-trigger]')
  useEffect(() => {
    ref.current?.focus()
    c.markRead(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  if (!entry) return null
  const alert = entry.alert
  const status = entry.status
  const closed = status === 'dismissed' || status === 'settled'
  const handled = closed || status === 'snoozed'
  const sev = SEVERITY_STYLE[alert.severity]
  const approval = detail?.approval ?? null
  const when = (t: number) => new Date(t).toLocaleString([], { hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric' })

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onClose()
      return
    }
    if (e.key !== 'Tab') return
    const f = ref.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input, select, textarea, a[href], [tabindex]:not([tabindex="-1"])')
    if (!f?.length) return
    const first = f[0]
    const last = f[f.length - 1]
    if (e.shiftKey && (document.activeElement === first || document.activeElement === ref.current)) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  const handleItems = [
    ...SNOOZES.map((x) => ({ label: `Snooze ${x.label}`, run: () => c.snooze(id, x.ms) })),
    { label: 'Snooze until tomorrow 09:00', run: () => c.snooze(id, untilTomorrow(c.now)) },
    ...(c.settle ? [{ label: 'Mark handled', run: () => c.settle?.(id) }] : []),
    { label: 'Dismiss', run: () => { c.dismiss(id); onClose() } },
  ]
  const showActions = !(approval && !closed)

  return (
    <div data-overlay="" className="fixed inset-0 z-[60] flex justify-end bg-bg/60" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={`Alert: ${alert.title}`}
        data-testid="alert-detail"
        data-status={status}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="relative flex h-full w-full flex-col border-l border-border bg-bg-elevated outline-none md:max-w-xl"
      >
        <header className="flex items-center gap-2 border-b border-border-subtle px-4 py-2.5">
          <AlertIcon icon={alert.icon} severity={alert.severity} />
          <span className="min-w-0 truncate font-mono dash-caption uppercase tracking-label text-fg-muted">
            {sev.label} <span className="normal-case tracking-normal text-fg-faint">{alert.kind}</span>
          </span>
          <span className="ml-auto shrink-0 dash-support text-fg-muted">{ago(alert.ts, c.now)}</span>
          <Button type="button" variant="ghost" size="icon-xs" onClick={onClose} aria-label="Close alert detail" className="text-fg-muted">
            <X aria-hidden="true" />
          </Button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto">
          <section className="space-y-2 px-4 py-4">
            <div className="flex items-start gap-2">
              <h2 className="min-w-0 flex-1 break-words font-sans dash-body font-semibold normal-case tracking-normal text-fg">{alert.title}</h2>
              {handled && (
                <Pill tone="neutral" className="shrink-0">
                  <span data-testid="alert-status">{status === 'snoozed' && entry.snoozeUntil ? `snoozed until ${when(entry.snoozeUntil)}` : (entry.resolution ?? status)}</span>
                </Pill>
              )}
            </div>
            {alert.detail && <p className="break-words dash-control text-fg-muted"><Linkified text={alert.detail} /></p>}
          </section>

          {approval &&
            (approval.provider_handoff ? (
              <section aria-label="Approval" className="px-4 pb-4">
                <p className="rounded border border-border bg-surface/40 p-3 dash-support text-fg-muted">Approved through the provider. Nothing to decide here.</p>
              </section>
            ) : (
              <section aria-label="Decision" className="border-y border-border-subtle">
                <DecisionDetail id={approval.item_id} client={approvals} onChanged={() => { void queue?.refresh(true); load() }} />
              </section>
            ))}

          {failed && !detail && <p role="status" className="px-4 py-3 dash-support text-fg-muted">Could not load the details. <button type="button" className="underline" onClick={load}>Try again</button></p>}

          {!!detail?.timeline.length && (
            <section aria-label="Timeline" className="px-4 pb-4">
              <h3 className={H}>What happened</h3>
              <ol className="mt-3 space-y-3 border-l border-border pl-4">
                {detail.timeline.map((t, i) => {
                  const last = i === detail.timeline.length - 1
                  return (
                    <li key={`${t.at_ms}:${i}`} className="relative dash-control text-fg">
                      <span aria-hidden className={`absolute -left-[calc(1rem+4px)] top-1.5 size-2 rounded-full border border-fg-muted ${last ? 'bg-fg' : 'bg-bg-elevated'}`} />
                      {t.label} <span className="text-fg-muted">· {ago(t.at_ms, c.now)}</span>
                    </li>
                  )
                })}
              </ol>
            </section>
          )}

          {(!!detail?.linked.length || alert.primary) && (
            <section aria-label="Linked" className="px-4 pb-4">
              <h3 className={H}>Linked</h3>
              <ul className="mt-2 flex flex-wrap items-center gap-2">
                {detail?.linked.map((s) => {
                  const href = safeHref(s.url)
                  const text = s.label && s.label !== s.ref ? s.label : s.ref
                  return (
                    <li key={`${s.kind}:${s.ref}`}>
                      <Chip className="py-0.5">
                        <span className="text-fg-faint">{s.kind}</span>
                        {href ? (
                          <a href={href} className="underline decoration-fg-faint hover:decoration-fg-secondary" {...(href.startsWith('/') ? {} : { target: '_blank', rel: 'noopener noreferrer' })}>{text}</a>
                        ) : (
                          <Linkified text={text} />
                        )}
                      </Chip>
                    </li>
                  )
                })}
              </ul>
            </section>
          )}

          {detail && detail.technical.fields.length > 0 && (
            <details className="px-4 pb-4">
              <summary className="cursor-pointer font-mono dash-caption uppercase tracking-label text-fg-muted">Technical details</summary>
              <dl aria-label="Details" className="mt-2 grid grid-cols-[minmax(6rem,9rem)_1fr] gap-x-3 gap-y-1">
                <dt className="truncate font-mono text-label text-fg-muted">kind</dt>
                <dd className="min-w-0 break-words font-mono text-xs text-fg">{alert.kind}</dd>
                {detail.technical.fields.map((f) => (
                  <div key={f.label} className="contents">
                    <dt className="truncate font-mono text-label text-fg-muted">{f.label}</dt>
                    <dd className="min-w-0 break-words font-mono text-xs text-fg"><Linkified text={f.value} /></dd>
                  </div>
                ))}
              </dl>
            </details>
          )}
        </div>

        <footer role="toolbar" aria-label="Alert actions" className="flex items-center gap-2 border-t border-border-subtle px-4 py-3">
          {alert.primary && <AlertLinkButton link={alert.primary} onFollow={onClose} />}
          {showActions && (closed || status === 'snoozed' ? (
            <Button type="button" variant="outline" size="sm" onClick={() => c.restore(id)}>{closed ? 'Restore' : 'Bring back now'}</Button>
          ) : (
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button type="button" variant="outline" size="sm" aria-label="Handle" />}>Handle ▾</DropdownMenuTrigger>
              <DropdownMenuContent aria-label="Handle" align="start" data-dashboard-shape="square" className="w-60">
                {handleItems.map((h) => <DropdownMenuItem key={h.label} onClick={h.run}>{h.label}</DropdownMenuItem>)}
              </DropdownMenuContent>
            </DropdownMenu>
          ))}
          {approval && status === 'settled' && <span className="dash-support text-fg-muted">Settled by approval</span>}
          {c.error && <span role="status" className="dash-support text-fg-muted">{c.error}</span>}
          <span className="ml-auto dash-support text-fg-faint">Esc close</span>
        </footer>
      </div>
    </div>
  )
}
