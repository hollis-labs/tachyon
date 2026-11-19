import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Input, Pill, Textarea } from '@hollis-labs/design-components'
import { X } from 'lucide-react'
import { ago } from '@console/lib/agent-activity'
import { Linkified } from '../../autolink'
import { useConsoleState } from '../../events/hooks'
import type { ConsoleEvent } from '../../events/types'
import { MessageComposer } from '../../messaging'
import { useMessaging } from '../../messaging/context'
import type { Recipient } from '../../messaging/types'
import type { ItemStatus } from '../../seams/state-store'
import { useAlertsController } from '../context'
import { AlertIcon } from '../AlertIcon'
import { AlertLinkButton } from '../AlertLinkButton'
import { safeHref } from '../policy'
import { SEVERITY_STYLE } from '../severity-style'
import { defaultAlertMapper } from '../mapping'
import type { Alert, AlertMapper } from '../types'
import { DEFER_CONTEXTS, defaultAlertActions, deferPresets, snoozePresets, type AlertActionId, type AlertActionResolver, type AlertActionSpec } from './actions'
import { relatedEvents, replyTarget, structuredFields } from './detail-data'
import { useAlertActions } from './use-alert-actions'
import { useRestoreFocus } from '../../shell/restore-focus'

const VARIANT: Record<AlertActionSpec['intent'], 'default' | 'outline' | 'destructive'> = { primary: 'default', neutral: 'outline', danger: 'destructive' }
const FOCUS = 'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'
const MONO = ''
const H = 'font-sans text-xs font-medium normal-case tracking-normal text-fg-muted'

type Panel = null | 'snooze' | 'defer' | 'settle' | 'reply' | 'decide'

export interface AlertDetailModalProps {
  /** Override the per-kind action declaration. */
  actions?: AlertActionResolver
  mapper?: AlertMapper
}

/** Mounted by <AlertHost>; opens when `expandedId` is set on the alerts controller. */
export function AlertDetailModal(props: AlertDetailModalProps) {
  const c = useAlertsController()
  const entry = c.expandedId ? c.entries.find((e) => e.alert.id === c.expandedId) : undefined
  if (!entry) return null
  return <AlertDetail key={entry.alert.id} alert={entry.alert} status={entry.status} snoozeUntil={entry.snoozeUntil} resolution={entry.resolution} onClose={() => c.expand(null)} {...props} />
}

interface DetailProps extends AlertDetailModalProps {
  alert: Alert
  status: ItemStatus
  snoozeUntil?: number
  resolution?: string
  onClose: () => void
}

export function AlertDetail({ alert, status, snoozeUntil, resolution, onClose, actions = defaultAlertActions, mapper = defaultAlertMapper }: DetailProps) {
  const { events } = useConsoleState()
  const event = useMemo(() => events.find((e) => e.id === alert.id), [events, alert.id])
  const runner = useAlertActions(alert, event, { status })
  const shown = runner.optimistic ?? { status, snoozeUntil, resolution }
  const closed = shown.status === 'dismissed' || shown.status === 'settled'
  const handled = closed || shown.status === 'snoozed'
  const specs = useMemo(() => actions(alert, event), [actions, alert, event])
  const [panel, setPanel] = useState<Panel>(null)
  const [decide, setDecide] = useState<AlertActionSpec | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const sev = SEVERITY_STYLE[alert.severity]
  const { markRead, now } = useAlertsController()

  useRestoreFocus(true, '[data-alert-trigger]')
  useEffect(() => {
    ref.current?.focus()
    markRead(alert.id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [alert.id])

  // An action can remove the button that had focus (approve -> settled): pull focus back into the dialog
  // so Escape and the Tab trap keep working.
  useEffect(() => {
    const el = ref.current
    if (el && !el.contains(document.activeElement)) el.focus()
  }, [shown.status, panel, runner.pending])

  const choose = (spec: AlertActionSpec) => {
    runner.clearMessages()
    if (spec.id === 'dismiss') void runner.run(spec).then((ok) => ok && onClose())
    else if (spec.id === 'approve' || spec.id === 'deny') {
      setDecide(spec)
      setPanel('decide')
    } else setPanel(panel === spec.id ? null : (spec.id as Panel))
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      if (panel) setPanel(null)
      else onClose()
      return
    }
    if (e.key === 'Tab') {
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
      return
    }
    const t = e.target as HTMLElement
    if (e.metaKey || e.ctrlKey || e.altKey || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable || closed || runner.pending) return
    const hit = specs.find((s) => s.shortcut === e.key.toLowerCase())
    if (hit) {
      e.preventDefault()
      choose(hit)
    }
  }

  const fields = useMemo(() => structuredFields(event), [event])
  const related = useMemo(() => relatedEvents(events, event), [events, event])
  const refs = event?.subjects ?? []

  return (
    <div data-overlay="" className="fixed inset-0 z-[60] flex justify-end bg-bg/60" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={`Alert: ${alert.title}`}
        data-testid="alert-detail"
        data-status={shown.status}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="relative h-full w-full overflow-y-auto border-l border-border bg-bg-elevated outline-none md:max-w-xl"
      >
        <header className="sticky top-0 z-10 flex items-center gap-2 border-b border-border-subtle bg-bg-elevated px-4 py-2.5">
          <AlertIcon icon={alert.icon} severity={alert.severity} />
          {sev.named && <span className={`text-xs ${sev.text}`}>{sev.label}</span>}
          <span className="ml-auto text-xs text-fg-muted">{ago(alert.ts, now)}</span>
          <Button type="button" variant="ghost" size="icon-xs" onClick={onClose} aria-label="Close alert detail" className="text-fg-muted">
            <X aria-hidden="true" />
          </Button>
        </header>

        <section className="border-b border-border-subtle px-4 py-3">
          <div className="flex items-start gap-2">
            <h2 className="min-w-0 flex-1 break-words font-sans text-base font-semibold normal-case tracking-normal text-fg">{alert.title}</h2>
            {handled && (
              <Pill tone="neutral" className="shrink-0"><span data-testid="alert-status">
                {shown.status === 'snoozed' && shown.snoozeUntil ? `snoozed until ${new Date(shown.snoozeUntil).toLocaleString([], { hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric' })}` : (shown.resolution ?? shown.status)}
              </span></Pill>
            )}
          </div>
          {alert.detail && (
            <p className="mt-1 break-words text-xs text-fg-secondary">
              <Linkified text={alert.detail} />
            </p>
          )}
          <p className="mt-1 text-xs text-fg-muted">{alert.sourceLabel ?? alert.source}</p>
        </section>

        <details className="border-b border-border-subtle px-4 py-3">
          <summary className="cursor-pointer text-xs font-medium text-fg-muted">Technical details</summary>
          <dl aria-label="Details" className="mt-2 grid grid-cols-[minmax(6rem,9rem)_1fr] gap-x-3 gap-y-1">
            <dt className="truncate font-mono text-label text-fg-muted">kind</dt>
            <dd className="min-w-0 break-words font-mono text-xs text-fg">{alert.kind}</dd>
            {fields.map((f) => (
              <div key={f.label} className="contents">
                <dt className="truncate font-mono text-label text-fg-muted">{f.label}</dt>
                <dd className="min-w-0 break-words font-mono text-xs text-fg">
                  <Linkified text={f.value} />
                </dd>
              </div>
            ))}
          </dl>
        </details>

        {(refs.length > 0 || alert.primary) && (
          <section aria-label="Linked" className="border-b border-border-subtle px-4 py-3">
            <h3 className={H}>Linked</h3>
            <ul className="mt-2 flex flex-wrap items-center gap-2">
              {refs.map((s) => {
                const href = safeHref(s.url)
                return (
                  <li key={`${s.type}:${s.id}`} className="flex items-center gap-1.5 font-mono text-xs text-fg">
                    <span className="text-xs text-fg-muted">{s.type}</span>
                    {href ? (
                      <a href={href} className="underline decoration-fg-faint hover:decoration-fg-secondary" {...(href.startsWith('/') ? {} : { target: '_blank', rel: 'noopener noreferrer' })}>
                        {s.label ?? s.id}
                      </a>
                    ) : (
                      <Linkified text={s.label && s.label !== s.id ? `${s.label} ${s.id}` : s.id} />
                    )}
                  </li>
                )
              })}
              {alert.primary && (
                <li>
                  <AlertLinkButton link={alert.primary} onFollow={onClose} />
                </li>
              )}
            </ul>
          </section>
        )}

        <section aria-label="Timeline" className="border-b border-border-subtle px-4 py-3">
          <h3 className={H}>Timeline</h3>
          <ol className="mt-2 space-y-1">
            {related.map((e) => (
              <TimelineRow key={e.id} e={e} mapper={mapper} now={now} />
            ))}
            <li className="flex gap-3 font-mono text-xs text-fg">
              <span className="w-16 shrink-0 text-fg-muted">{ago(alert.ts, now)}</span>
              <span>{humanKind(alert.kind)} (this alert)</span>
            </li>
          </ol>
        </section>

        <ActionBar specs={specs} onPick={choose} disabled={runner.pending !== null || closed} active={panel} pending={runner.pending} />

        {panel === 'snooze' && <TimePanel title="Snooze until" presets={snoozePresets(now)} customLabel="minutes" onPick={(until) => void runner.run(specOf(specs, 'snooze'), { until }).then((ok) => ok && setPanel(null))} busy={runner.pending !== null} />}
        {panel === 'defer' && <DeferPanel onPick={(i) => void runner.run(specOf(specs, 'defer'), i).then((ok) => ok && setPanel(null))} busy={runner.pending !== null} />}
        {panel === 'settle' && <NotePanel title="Settle with note" cta="Settle" onSubmit={(note) => void runner.run(specOf(specs, 'settle'), { note }).then((ok) => ok && setPanel(null))} busy={runner.pending !== null} />}
        {panel === 'decide' && decide && (
          <NotePanel
            title={decide.id === 'approve' ? 'Approve' : 'Deny'}
            cta={decide.confirm ? `${decide.label} (confirm identity)` : decide.label}
            optional
            intent={decide.intent}
            onSubmit={(note) => void runner.run(decide, { note }).then((ok) => ok && setPanel(null))}
            busy={runner.pending !== null}
          />
        )}
        {panel === 'reply' && <ReplyPanel key={alert.id} alert={alert} event={event} onDone={() => setPanel(null)} />}

        <div className="min-h-[2rem] border-t border-border-subtle px-4 py-1.5 font-mono text-label">
          {runner.error ? (
            <span role="alert" className="text-danger">{runner.error}</span>
          ) : runner.notice ? (
            <span role="status" className="text-success">{runner.notice}</span>
          ) : (
            <span className="hidden text-fg-muted md:inline">{specs.map((s) => `${s.shortcut.toUpperCase()} ${s.label.toLowerCase()}`).join(' · ')} · Esc close</span>
          )}
          {closed && (
            <Button type="button" variant="outline" size="xs" onClick={() => void runner.restore()} className={`ml-3 ${MONO}`}>
              Restore
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

/** 'approval.requested' -> 'Approval requested'. */
const humanKind = (kind: string) => {
  const t = kind.replace(/[._]/g, ' ').trim()
  return t.charAt(0).toUpperCase() + t.slice(1)
}

const specOf = (specs: AlertActionSpec[], id: AlertActionId): AlertActionSpec => specs.find((s) => s.id === id) ?? { id, label: id, intent: 'neutral', shortcut: '' }

function TimelineRow({ e, mapper, now }: { e: ConsoleEvent; mapper: AlertMapper; now: number }) {
  const m = mapper(e)
  return (
    <li className="flex gap-3 font-mono text-xs text-fg-secondary">
      <span className="w-16 shrink-0 text-fg-muted">{ago(e.ts, now)}</span>
      <span className="min-w-0 break-words">{m?.title ?? humanKind(e.kind)}</span>
    </li>
  )
}

function ActionBar({ specs, onPick, disabled, active, pending }: { specs: AlertActionSpec[]; onPick: (s: AlertActionSpec) => void; disabled: boolean; active: Panel; pending: string | null }) {
  // Approve and Deny are the visible decision; everything else lives under More. An alert with no
  // decision shows its first action.
  const decision = specs.filter((s) => s.id === 'approve' || s.id === 'deny')
  const visible = decision.length ? decision : specs.slice(0, 1)
  const more = specs.filter((s) => !visible.includes(s))
  return (
    <div role="toolbar" aria-label="Alert actions" className="flex flex-wrap items-center gap-2 px-4 py-3">
      {visible.map((s) => (
        <Button
          key={s.id}
          type="button"
          variant={VARIANT[s.intent]}
          size="sm"
          disabled={disabled}
          aria-pressed={active === s.id}
          aria-keyshortcuts={s.shortcut}
          onClick={() => onPick(s)}
        >
          {pending === s.id ? `${s.label}...` : s.label}
          {s.confirm && <span className="sr-only"> (requires identity confirmation)</span>}
        </Button>
      ))}
      {more.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button type="button" variant="outline" size="sm" disabled={disabled} aria-label="More actions" />}>More</DropdownMenuTrigger>
          <DropdownMenuContent aria-label="More actions" align="start" data-dashboard-shape="square" className="w-40">
            {more.map((s) => (
              <DropdownMenuItem key={s.id} onClick={() => onPick(s)}>{pending === s.id ? `${s.label}...` : s.label}</DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}

function PanelShell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="border-t border-border-subtle bg-surface/40 px-4 py-3">
      <h3 className={H}>{title}</h3>
      <div className="mt-2">{children}</div>
    </section>
  )
}

function TimePanel({ title, presets, onPick, busy, customLabel }: { title: string; presets: { id: string; label: string; until: number }[]; onPick: (until: number) => void; busy: boolean; customLabel: string }) {
  const [custom, setCustom] = useState('')
  const mins = Number(custom)
  return (
    <PanelShell title={title}>
      <div className="flex flex-wrap items-center gap-2">
        {presets.map((p) => (
          <Button key={p.id} type="button" variant="outline" size="xs" disabled={busy} onClick={() => onPick(p.until)}>
            {p.label}
          </Button>
        ))}
        <label className="flex items-center gap-1.5 font-mono text-label text-fg-secondary">
          Custom
          <Input type="number" min={1} value={custom} onChange={(e) => setCustom(e.target.value)} aria-label={`Custom ${customLabel}`} className="h-7 w-20 font-mono text-xs" placeholder={customLabel} />
          <Button type="button" variant="outline" size="xs" disabled={busy || !(mins > 0)} onClick={() => onPick(Date.now() + mins * 60_000)}>
            Set
          </Button>
        </label>
      </div>
    </PanelShell>
  )
}

function DeferPanel({ onPick, busy }: { onPick: (i: { until?: number; context?: string }) => void; busy: boolean }) {
  const [mode, setMode] = useState<'time' | 'context'>('time')
  return (
    <PanelShell title="Defer">
      <div role="tablist" aria-label="Defer to" className="mb-2 flex gap-4 text-xs">
        {(['time', 'context'] as const).map((m) => (
          <button key={m} role="tab" type="button" aria-selected={mode === m} onClick={() => setMode(m)} className={`border-b-2 pb-0.5 ${FOCUS} ${mode === m ? 'border-fg text-fg' : 'border-transparent text-fg-muted hover:text-fg-secondary'}`}>
            To a {m}
          </button>
        ))}
      </div>
      {mode === 'time' ? (
        <TimePanelBody onPick={(until) => onPick({ until })} busy={busy} />
      ) : (
        <div className="flex flex-wrap gap-2">
          {DEFER_CONTEXTS.map((c) => (
            <Button key={c.id} type="button" variant="outline" size="xs" disabled={busy} onClick={() => onPick({ context: c.label })}>
              {c.label}
            </Button>
          ))}
        </div>
      )}
    </PanelShell>
  )
}

function TimePanelBody({ onPick, busy }: { onPick: (until: number) => void; busy: boolean }) {
  const [mountedAt] = useState(() => Date.now())
  const [at, setAt] = useState('')
  const when = at ? new Date(at).getTime() : NaN
  return (
    <div className="flex flex-wrap items-center gap-2">
      {deferPresets(mountedAt).map((p) => (
        <Button key={p.id} type="button" variant="outline" size="xs" disabled={busy} onClick={() => onPick(p.until)}>
          {p.label}
        </Button>
      ))}
      <Input type="datetime-local" aria-label="Defer until" value={at} onChange={(e) => setAt(e.target.value)} className="h-7 w-auto font-mono text-xs" />
      <Button type="button" variant="outline" size="xs" disabled={busy || !(when > mountedAt)} onClick={() => onPick(when)}>
        Set
      </Button>
    </div>
  )
}

function NotePanel({ title, cta, onSubmit, busy, optional = true, intent = 'primary' }: { title: string; cta: string; onSubmit: (note: string | undefined) => void; busy: boolean; optional?: boolean; intent?: AlertActionSpec['intent'] }) {
  const [note, setNote] = useState('')
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <PanelShell title={title}>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          onSubmit(note.trim() || undefined)
        }}
        className="flex flex-col gap-2"
      >
        <Textarea ref={ref} value={note} onChange={(e) => setNote(e.target.value)} rows={2} maxLength={2000} aria-label="Note" placeholder={optional ? 'Note (optional)' : 'Note'} className="w-full resize-y font-mono text-xs" />
        <div>
          <Button type="submit" variant={VARIANT[intent]} size="sm" disabled={busy} className={MONO}>
            {busy ? 'Working...' : cta}
          </Button>
        </div>
      </form>
    </PanelShell>
  )
}

/** Opens the M1 composer in-thread. The recipient is the agent / sender the alert came from. */
function ReplyPanel({ alert, event, onDone }: { alert: Alert; event?: ConsoleEvent; onDone: () => void }) {
  const { client } = useMessaging()
  const { markRead } = useAlertsController()
  const ref = replyTarget(alert, event)
  const [recipient, setRecipient] = useState<Recipient | null | undefined>(ref ? undefined : null)
  useEffect(() => {
    let live = true
    if (!ref) return
    client.resolveRecipient(ref).then(
      (r) => live && setRecipient(r),
      () => live && setRecipient(null),
    )
    return () => {
      live = false
    }
  }, [client, ref])
  return (
    <PanelShell title="Reply">
      {recipient === undefined ? (
        <p className="font-mono text-xs text-fg-muted">Resolving recipient...</p>
      ) : recipient === null ? (
        <p role="alert" className="font-mono text-xs text-danger">No messageable sender for this alert.</p>
      ) : (
        <MessageComposer
          recipient={recipient}
          replyTo={{ id: alert.id, threadId: alert.threadId }}
          subject={alert.title}
          onSent={() => {
            markRead(alert.id)
            onDone()
          }}
          onCancel={onDone}
        />
      )}
    </PanelShell>
  )
}
