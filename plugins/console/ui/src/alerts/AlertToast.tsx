import { useEffect, useRef, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { X } from 'lucide-react'
import { AlertIcon } from './AlertIcon'
import { AlertLinkButton } from './AlertLinkButton'
import { TOAST_EXPIRE_MS } from './policy'
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
  const title = alert.count && alert.count > 1 ? `${alert.title} ×${alert.count}` : alert.title
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
      className="pointer-events-auto relative flex gap-3 overflow-hidden border border-border bg-bg-elevated px-3 py-2.5 shadow-[0_8px_24px_rgb(0_0_0/calc(.3*var(--sh)))] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
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
          <button type="button" onClick={() => { onRead?.(); onClose(); onExpand() }} aria-label={`Open details: ${alert.title}`} className="block w-full truncate text-left dash-control font-semibold text-fg hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">{title}</button>
        ) : (
          <p className="truncate dash-control font-semibold text-fg">{title}</p>
        )}
        {alert.detail && <p className="truncate dash-support text-fg-muted"><LinkText surface="alerts" text={alert.detail} /></p>}
        {(alert.primary || total === null) && (
          <div className="mt-1.5 flex flex-wrap items-center gap-2">
            {alert.primary && (
              <AlertLinkButton
                link={alert.primary}
                onFollow={() => {
                  onRead?.()
                  onClose()
                }}
              />
            )}
            {total === null && <span className="dash-support text-fg-faint">Stays until you dismiss it</span>}
          </div>
        )}
      </div>
      <Button type="button" variant="ghost" size="icon-xs" onClick={onClose} aria-label={`Close alert: ${alert.title}`} className="shrink-0 text-fg-muted">
        <X aria-hidden="true" />
      </Button>
    </div>
  )
}
