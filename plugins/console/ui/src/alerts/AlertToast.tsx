import { useEffect, useRef, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { X } from 'lucide-react'
import { ago } from '@console/lib/agent-activity'
import { AlertIcon } from './AlertIcon'
import { AlertLinkButton } from './AlertLinkButton'
import { TOAST_EXPIRE_MS } from './policy'
import { SEVERITY_STYLE } from './severity-style'
import type { Alert } from './types'
import { LinkText } from '../autolink/LinkText'

export interface AlertToastProps {
  alert: Alert
  now: number
  /** Override auto-expire (ms, null = sticky). Defaults to the per-severity policy. */
  durationMs?: number | null
  onClose: () => void
  onRead?: () => void
  /** Opens the alert in the detail modal (A2). */
  onExpand?: () => void
}

/** Compact toast card. Expires per severity; hover or focus pauses the clock; Escape closes. */
export function AlertToast({ alert, now, durationMs, onClose, onRead, onExpand }: AlertToastProps) {
  const sev = SEVERITY_STYLE[alert.severity]
  const total = durationMs === undefined ? TOAST_EXPIRE_MS[alert.severity] : durationMs
  const [paused, setPaused] = useState(false)
  const remaining = useRef(total)
  const closeRef = useRef(onClose)
  useEffect(() => {
    closeRef.current = onClose
  })

  useEffect(() => {
    if (paused || remaining.current === null) return
    const left = remaining.current
    const started = Date.now()
    const t = setTimeout(() => closeRef.current(), left)
    return () => {
      clearTimeout(t)
      remaining.current = Math.max(0, left - (Date.now() - started))
    }
  }, [paused])

  const urgent = alert.severity === 'error' || alert.severity === 'critical'
  return (
    <div
      role={urgent ? 'alert' : 'status'}
      data-testid="alert-toast"
      data-severity={alert.severity}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose()
      }}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
      className="pointer-events-auto relative flex gap-3 overflow-hidden border border-border bg-bg-elevated px-3 py-2.5 shadow-lg shadow-black/30 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {total !== null && (
        // Hairline countdown; pauses with the timer and is dropped under reduced motion.
        <>
          <style>{'@keyframes alert-countdown{from{transform:scaleX(1)}to{transform:scaleX(0)}}@media (prefers-reduced-motion:reduce){.alert-countdown{display:none}}'}</style>
          <span aria-hidden="true" data-testid="alert-countdown" className="alert-countdown absolute inset-x-0 bottom-0 h-px origin-left bg-fg-faint" style={{ animation: `alert-countdown ${total}ms linear forwards`, animationPlayState: paused ? 'paused' : 'running' }} />
        </>
      )}
      <span className="pt-0.5">
        <AlertIcon icon={alert.icon} severity={alert.severity} />
      </span>
      <div className="min-w-0 flex-1">
        {onExpand ? (
          <button type="button" onClick={() => { onRead?.(); onClose(); onExpand() }} aria-label={`Open details: ${alert.title}`} className="block w-full truncate text-left text-[13px] font-semibold text-fg hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">{alert.title}</button>
        ) : (
          <p className="truncate text-[13px] font-semibold text-fg">{alert.title}</p>
        )}
        {alert.detail && <p className="truncate text-xs text-fg-muted"><LinkText surface="alerts" text={alert.detail} /></p>}
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-fg-faint">
          {sev.named && <span className={sev.text}>{sev.label}</span>}
          <span>{alert.source}</span>
          <span>{ago(alert.ts, now)}</span>
          {total === null && <span>stays until dismissed</span>}
        </p>
        {alert.primary && (
          <div className="mt-1.5">
            <AlertLinkButton
              link={alert.primary}
              onFollow={() => {
                onRead?.()
                onClose()
              }}
            />
          </div>
        )}
      </div>
      <Button type="button" variant="ghost" size="icon-xs" onClick={onClose} aria-label={`Close alert: ${alert.title}`} className="shrink-0 text-fg-muted">
        <X aria-hidden="true" />
      </Button>
    </div>
  )
}
