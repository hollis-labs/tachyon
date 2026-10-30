import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Button, Textarea, cn } from '@hollis-labs/design-components'
import { Kbd } from '@planner/shared'
import { AgentsApiError, agentsGet, agentsPost, useAgentsRead, type AgentsSettings, type ControlRequest } from './api'
import { AGENTS } from './vocabulary'
import type { Session } from './types'

const newId = () => (typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `req_${Date.now()}_${Math.random().toString(36).slice(2)}`)
const V = AGENTS.nudge
const R = AGENTS.restart

/**
 * Backend A6 (Nudge/Restart) is not approved yet: until it is, both controls render disabled with a
 * reason and nothing is sent. Set VITE_AGENTS_CONTROLS=1 on a dev instance to review the popovers.
 */
export const CONTROLS_LIVE = (import.meta.env?.VITE_AGENTS_CONTROLS as string | undefined) === '1'

interface ControlProps {
  session: Session
  /** Reconnecting: controls are disabled with a visible reason. */
  paused: boolean
  /** Defaults to CONTROLS_LIVE; tests pass it explicitly. */
  live?: boolean
}

/** Why a control cannot be used right now, or null. */
function blocked(paused: boolean, allowed: boolean, what: string, live: boolean): string | null {
  if (!live) return AGENTS.modal.controlsPending
  if (paused) return AGENTS.modal.controlsPaused
  if (!allowed) return AGENTS.modal.notAllowed(what)
  return null
}


/**
 * A panel anchored under its trigger, drawn inside the modal. KIT-GAP: the kit Popover portals to a layer
 * that sits under an open Dialog, so it cannot be used from the session modal; this stands in until
 * the kit's Popover layers above Dialog. Esc closes the panel first (the modal stays), focus returns to the trigger.
 */
function ControlPanel({ open, onOpenChange, label, trigger, children }: { open: boolean; onOpenChange: (o: boolean) => void; label: string; trigger: (props: { onClick: () => void; ref: React.RefObject<HTMLButtonElement | null> }) => ReactNode; children: ReactNode }) {
  const root = useRef<HTMLSpanElement>(null)
  const btn = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (!open) return
    const out = (e: MouseEvent) => !root.current?.contains(e.target as Node) && onOpenChange(false)
    document.addEventListener('mousedown', out)
    return () => document.removeEventListener('mousedown', out)
  }, [open, onOpenChange])
  const was = useRef(open)
  useEffect(() => {
    if (was.current && !open) btn.current?.focus()
    was.current = open
  }, [open])
  return (
    <span ref={root} className="relative">
      {trigger({ onClick: () => onOpenChange(!open), ref: btn })}
      {open && (
        <div
          role="dialog"
          aria-label={label}
          onKeyDown={(e) => {
            if (e.key === 'Escape') (e.stopPropagation(), e.preventDefault(), onOpenChange(false))
          }}
          className="absolute right-0 top-full z-20 mt-1 flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-3 rounded-panel border border-surface-hover bg-bg-elevated p-3 shadow-lg max-sm:fixed max-sm:inset-x-0 max-sm:bottom-0 max-sm:top-auto max-sm:mt-0 max-sm:w-auto max-sm:rounded-b-none"
        >
          {children}
        </div>
      )}
    </span>
  )
}

export function NudgeControl({ session, paused, onSent, live = CONTROLS_LIVE }: ControlProps & { onSent: () => void }) {
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [interrupt, setInterrupt] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<AgentsApiError | null>(null)
  const inFlight = useRef(false)
  const requestId = useRef(newId())
  const settings = useAgentsRead<AgentsSettings>(open ? '/settings' : null)
  const why = blocked(paused, session.capabilities.nudge, 'be nudged', live)
  const intr = session.capabilities.interrupt

  const send = async (withInterrupt: boolean) => {
    if (inFlight.current || !text.trim()) return // never submit twice; the draft stays in the field on any failure
    inFlight.current = true
    setBusy(true)
    setErr(null)
    try {
      const r = await agentsPost<ControlRequest>(`/sessions/${encodeURIComponent(session.id)}/nudge`, { request_id: requestId.current, text: text.trim(), interrupt: withInterrupt })
      if (r.state === 'failed') throw new AgentsApiError(502, 'failed', r.error ?? V.failed)
      requestId.current = newId()
      setText('')
      setOpen(false)
      onSent()
    } catch (e) {
      if (e instanceof AgentsApiError && e.code === 'interrupt_unsupported') setInterrupt(false)
      setErr(e instanceof AgentsApiError ? e : new AgentsApiError(0, 'internal', V.failed))
      requestId.current = newId()
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  return (
    <ControlPanel
      open={open}
      onOpenChange={(o) => !busy && setOpen(o)}
      label={V.title}
      trigger={({ onClick, ref }) => <Button ref={ref} data-nudge-trigger size="sm" variant="outline" disabled={why !== null} title={why ?? undefined} aria-expanded={open} onClick={onClick}>{AGENTS.modal.nudge} ▾</Button>}
    >
      <>
        <label htmlFor="nudge-text" className="font-mono text-caption uppercase tracking-label text-fg-muted">{V.label}</label>
        <Textarea
          id="nudge-text"
          autoFocus
          rows={3}
          placeholder={V.placeholder(session.agent.name)}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) (e.preventDefault(), void send(interrupt))
          }}
        />
        <div className="flex flex-wrap gap-1.5">
          {(settings.data?.nudge_presets ?? []).map((p) => (
            <button key={p} type="button" onClick={() => setText(p)} className="h-5 rounded-full border border-border px-2 text-label text-fg-secondary hover:bg-surface-hover">{p}</button>
          ))}
        </div>
        {/* KIT-GAP: checkbox inside a popover (kit brief pending); native input until it lands. */}
        <label className={cn('flex items-center gap-2 text-xs', intr.available ? 'text-fg-secondary' : 'text-fg-faint')} title={intr.available ? undefined : V.interruptUnavailable(intr.reason ?? AGENTS.modal.unknown)}>
          <input type="checkbox" checked={interrupt} disabled={!intr.available} onChange={(e) => setInterrupt(e.target.checked)} />
          {V.interrupt}
        </label>
        {!intr.available && <p className="text-label text-fg-faint">{V.interruptUnavailable(intr.reason ?? AGENTS.modal.unknown)}</p>}
        {err && (
          <p role="alert" className="text-label text-danger">
            {err.message}{' '}
            {err.code === 'interrupt_unsupported' && <button type="button" className="underline" onClick={() => void send(false)}>{V.sendWithout}</button>}
          </p>
        )}
        <div className="flex items-center gap-2">
          <span className="text-label text-fg-muted">{interrupt ? V.hintInterrupt : V.hint}</span>
          <span className="flex-1" />
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setOpen(false)}>{V.cancel}</Button>
          <Button size="sm" disabled={busy || !text.trim()} onClick={() => void send(interrupt)}>{busy ? V.sending : <>{V.send} <Kbd className="border-current/30 text-current">⌘↵</Kbd></>}</Button>
        </div>
      </>
    </ControlPanel>
  )
}

const TERMINAL = new Set(['done', 'failed'])

export function RestartControl({ session, paused, onDone, live = CONTROLS_LIVE }: ControlProps & { onDone: (newSessionId: string | null) => void }) {
  const [open, setOpen] = useState(false)
  const [handoff, setHandoff] = useState(true)
  const [req, setReq] = useState<ControlRequest | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const inFlight = useRef(false)
  const requestId = useRef(newId())
  const settings = useAgentsRead<AgentsSettings>(open ? '/settings' : null)
  const why = blocked(paused, session.capabilities.restart, 'be restarted', live)
  const running = req !== null && !TERMINAL.has(req.state)

  // Follow the restart state machine until it ends. Cancel is only offered before the confirm.
  useEffect(() => {
    if (!req || TERMINAL.has(req.state)) return
    const t = setInterval(async () => {
      try {
        const next = await agentsGet<ControlRequest>(`/controls/${encodeURIComponent(req.request_id)}`)
        setReq(next)
        if (next.state === 'done') {
          setOpen(false)
          setReq(null)
          requestId.current = newId()
          onDone(next.new_session_id ?? null)
        }
      } catch {
        /* the next tick tries again; the state shown stays the last known */
      }
    }, 1500)
    return () => clearInterval(t)
  }, [req, onDone])

  const confirm = async () => {
    if (inFlight.current) return
    inFlight.current = true
    setErr(null)
    try {
      setReq(await agentsPost<ControlRequest>(`/sessions/${encodeURIComponent(session.id)}/restart`, { request_id: requestId.current, handoff, expected_session_id: session.id }))
    } catch (e) {
      setErr(e instanceof AgentsApiError && e.code === 'stale_session' ? R.stale : e instanceof Error ? e.message : R.failedAt('request'))
    } finally {
      inFlight.current = false
    }
  }
  const skip = async () => {
    if (!req) return
    try {
      setReq(await agentsPost<ControlRequest>(`/controls/${encodeURIComponent(req.request_id)}/skip`))
    } catch (e) {
      setErr(e instanceof Error ? e.message : '')
    }
  }
  const minutes = Math.max(1, Math.round((settings.data?.handoff_timeout_s ?? 300) / 60))
  const pendingNow = session.pending.known ? (session.pending.approval ? 1 : 0) + (session.pending.question ? 1 : 0) : null

  return (
    <ControlPanel
      open={open}
      onOpenChange={(o) => !running && setOpen(o)}
      label={R.title}
      trigger={({ onClick, ref }) => <Button ref={ref} size="sm" variant="outline" disabled={why !== null} title={why ?? undefined} className="border-danger/35 text-danger" aria-expanded={open} onClick={onClick}>{AGENTS.modal.restart}</Button>}
    >
      <>
        <h4 className="font-mono text-caption uppercase tracking-label text-fg-muted">{R.title}</h4>
        <p className="text-control text-fg">{R.explain} {R.discardsNote(pendingNow)}</p>
        {/* KIT-GAP: checkbox inside a popover (kit brief pending); native input until it lands. */}
        <label className="flex items-center gap-2 text-xs text-fg-secondary">
          <input type="checkbox" checked={handoff} disabled={req !== null} onChange={(e) => setHandoff(e.target.checked)} />
          {R.handoff}
        </label>
        {handoff && settings.data && <p className="rounded-control border border-border px-2.5 py-2 text-xs text-fg-secondary">{settings.data.handoff_prompt}</p>}
        {handoff && <p className="text-label text-fg-muted">{R.waitNote(minutes)}</p>}
        {req && (
          <p role="status" className="text-label text-fg-muted">
            {req.state === 'waiting' ? R.waiting(minutes) : R.steps[req.state] ?? req.state}
            {req.state === 'failed' && req.step ? `. ${R.failedAt(req.step)}` : ''}
            {req.discarded_pending ? `. ${R.discarded(req.discarded_pending)}` : ''}
          </p>
        )}
        {err && <p role="alert" className="text-label text-danger">{err}</p>}
        <div className="flex justify-end gap-2">
          {req?.state === 'waiting' && <Button size="sm" variant="ghost" onClick={() => void skip()}>{R.skip}</Button>}
          {!running && <Button size="sm" variant="ghost" onClick={() => (setReq(null), setOpen(false))}>{R.cancel}</Button>}
          {(req === null || req.state === 'failed') && (
            <Button size="sm" variant="outline" className="border-danger/35 text-danger" disabled={running} onClick={() => (req && (requestId.current = newId(), setReq(null)), void confirm())}>{R.confirm}</Button>
          )}
          {running && <Button size="sm" disabled>{R.working}</Button>}
        </div>
      </>
    </ControlPanel>
  )
}
