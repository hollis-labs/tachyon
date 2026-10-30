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
import { AlertLinkButton } from '../AlertLinkButton'
import { useAlertsController } from '../context'
import { safeHref } from '../policy'
import { SEVERITY_STYLE } from '../severity-style'
import { createAlertsClient, type AlertsClient } from './client'
import type { ServerAlertDetail as Detail } from './types'

const H = 'font-sans text-xs font-medium normal-case tracking-normal text-fg-muted'
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

  return (
    <div data-overlay="" className="fixed inset-0 z-[60] flex justify-end bg-black/60" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={`Alert: ${alert.title}`}
        data-testid="alert-detail"
        data-status={status}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="relative h-full w-full overflow-y-auto border-l border-border bg-bg-elevated outline-none md:max-w-xl"
      >
        <header className="sticky top-0 z-10 flex items-center gap-2 border-b border-border-subtle bg-bg-elevated px-4 py-2.5">
          <AlertIcon icon={alert.icon} severity={alert.severity} />
          {sev.named && <span className={`text-xs ${sev.text}`}>{sev.label}</span>}
          <span className="ml-auto text-xs text-fg-muted">{ago(alert.ts, c.now)}</span>
          <Button type="button" variant="ghost" size="icon-xs" onClick={onClose} aria-label="Close alert detail" className="text-fg-muted">
            <X aria-hidden="true" />
          </Button>
        </header>

        <section className="border-b border-border-subtle px-4 py-3">
          <div className="flex items-start gap-2">
            <h2 className="min-w-0 flex-1 break-words font-sans text-base font-semibold normal-case tracking-normal text-fg">{alert.title}</h2>
            {handled && (
              <Pill tone="neutral" className="shrink-0">
                <span data-testid="alert-status">{status === 'snoozed' && entry.snoozeUntil ? `snoozed until ${when(entry.snoozeUntil)}` : (entry.resolution ?? status)}</span>
              </Pill>
            )}
          </div>
          {alert.detail && <p className="mt-1 break-words text-xs text-fg-secondary"><Linkified text={alert.detail} /></p>}
          <p className="mt-1 text-xs text-fg-muted">{alert.source}</p>
        </section>

        {approval &&
          (approval.provider_handoff ? (
            <section aria-label="Approval" className="border-b border-border-subtle px-4 py-3">
              <p className="rounded border border-border bg-surface/40 p-3 text-xs text-fg-muted">Approved through the provider. Nothing to decide here.</p>
            </section>
          ) : (
            <section aria-label="Decision" className="border-b border-border-subtle">
              <DecisionDetail id={approval.item_id} client={approvals} onChanged={() => { void queue?.refresh(true); load() }} />
            </section>
          ))}

        {approval && !closed ? (
          c.error && <p role="status" className="border-b border-border-subtle px-4 py-3 text-xs text-fg-muted">{c.error}</p>
        ) : (
          <div role="toolbar" aria-label="Alert actions" className="flex flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-3">
            {closed || status === 'snoozed' ? (
              <>
                <Button type="button" variant="outline" size="sm" onClick={() => c.restore(id)}>{closed ? 'Restore' : 'Bring back now'}</Button>
                {approval && status === 'settled' && <span className="text-xs text-fg-muted">Settled by approval</span>}
              </>
            ) : (
              <>
                {c.settle && <Button type="button" size="sm" onClick={() => c.settle?.(id)}>Mark handled</Button>}
                <DropdownMenu>
                  <DropdownMenuTrigger render={<Button type="button" variant="outline" size="sm" aria-label="Snooze" />}>Snooze</DropdownMenuTrigger>
                  <DropdownMenuContent aria-label="Snooze" align="start" data-dashboard-shape="square" className="w-40">
                    {SNOOZES.map((s) => <DropdownMenuItem key={s.label} onClick={() => c.snooze(id, s.ms)}>{s.label}</DropdownMenuItem>)}
                    <DropdownMenuItem onClick={() => c.snooze(id, untilTomorrow(c.now))}>Tomorrow</DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
                <Button type="button" variant="outline" size="sm" onClick={() => { c.dismiss(id); onClose() }}>Dismiss</Button>
              </>
            )}
            {c.error && <span role="status" className="text-xs text-fg-muted">{c.error}</span>}
          </div>
        )}

        {failed && !detail && <p role="status" className="border-b border-border-subtle px-4 py-3 text-xs text-fg-muted">Could not load the details. <button type="button" className="underline" onClick={load}>Try again</button></p>}

        {detail && detail.technical.fields.length > 0 && (
          <details className="border-b border-border-subtle px-4 py-3">
            <summary className="cursor-pointer text-xs font-medium text-fg-muted">Technical details</summary>
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

        {(!!detail?.linked.length || alert.primary) && (
          <section aria-label="Linked" className="border-b border-border-subtle px-4 py-3">
            <h3 className={H}>Linked</h3>
            <ul className="mt-2 flex flex-wrap items-center gap-2">
              {detail?.linked.map((s) => {
                const href = safeHref(s.url)
                return (
                  <li key={`${s.kind}:${s.ref}`} className="flex items-center gap-1.5 font-mono text-xs text-fg">
                    <span className="text-xs text-fg-muted">{s.kind}</span>
                    {href ? (
                      <a href={href} className="underline decoration-fg-faint hover:decoration-fg-secondary" {...(href.startsWith('/') ? {} : { target: '_blank', rel: 'noopener noreferrer' })}>{s.label ?? s.ref}</a>
                    ) : (
                      <Linkified text={s.label && s.label !== s.ref ? `${s.label} ${s.ref}` : s.ref} />
                    )}
                  </li>
                )
              })}
              {alert.primary && <li><AlertLinkButton link={alert.primary} onFollow={onClose} /></li>}
            </ul>
          </section>
        )}

        {!!detail?.timeline.length && (
          <section aria-label="Timeline" className="px-4 py-3">
            <h3 className={H}>Timeline</h3>
            <ol className="mt-2 space-y-1">
              {detail.timeline.map((t, i) => (
                <li key={`${t.at_ms}:${i}`} className="flex gap-3 font-mono text-xs text-fg-secondary">
                  <span className="w-16 shrink-0 text-fg-muted">{ago(t.at_ms, c.now)}</span>
                  <span className="min-w-0 break-words">{t.label}</span>
                </li>
              ))}
            </ol>
          </section>
        )}
      </div>
    </div>
  )
}

