import { useEffect, useRef, useState } from 'react'
import { Button, Popover, PopoverContent, PopoverTrigger, Textarea } from '@hollis-labs/design-components'
import { AgentsApiError, agentsGet, agentsPost, useAgentsRead, type AgentsSettings, type ControlRequest } from './api'
import { AGENTS } from './vocabulary'
import type { Session } from './types'

const newId = () => (typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `req_${Date.now()}_${Math.random().toString(36).slice(2)}`)
const V = AGENTS.nudge
const R = AGENTS.restart

interface ControlProps {
  session: Session
  /** Reconnecting: controls are disabled with a visible reason. */
  paused: boolean
}

/** Why a control cannot be used right now, or null. */
function blocked(paused: boolean, allowed: boolean, what: string): string | null {
  if (paused) return AGENTS.modal.controlsPaused
  if (!allowed) return AGENTS.modal.notAllowed(what)
  return null
}

export function NudgeControl({ session, paused, onSent }: ControlProps & { onSent: () => void }) {
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [interrupt, setInterrupt] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<AgentsApiError | null>(null)
  const inFlight = useRef(false)
  const requestId = useRef(newId())
  const settings = useAgentsRead<AgentsSettings>(open ? '/settings' : null)
  const why = blocked(paused, session.capabilities.nudge, 'be nudged')
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
    <Popover open={open} onOpenChange={(o) => !busy && setOpen(o)}>
      <PopoverTrigger render={<Button size="sm" variant="outline" disabled={why !== null} title={why ?? undefined} />}>{AGENTS.modal.nudge}</PopoverTrigger>
      <PopoverContent align="end" className="flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2" role="dialog" aria-label={V.title}>
        <label htmlFor="nudge-text" className="dash-caption text-fg-muted">{V.label}</label>
        <Textarea
          id="nudge-text"
          autoFocus
          rows={4}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) (e.preventDefault(), void send(interrupt))
          }}
        />
        <div className="flex flex-wrap gap-1">
          {(settings.data?.nudge_presets ?? []).map((p) => (
            <button key={p} type="button" onClick={() => setText(p)} className="rounded-full border border-border px-2 py-0.5 dash-caption text-fg-muted hover:bg-panel-hover">{p}</button>
          ))}
        </div>
        <label className="flex items-center gap-2 dash-support text-fg">
          <input type="checkbox" checked={interrupt} disabled={!intr.available} onChange={(e) => setInterrupt(e.target.checked)} />
          {V.interrupt}
        </label>
        {!intr.available && <p className="dash-caption text-fg-faint">{V.interruptUnavailable(intr.reason ?? AGENTS.modal.unknown)}</p>}
        {err && (
          <p role="alert" className="dash-caption text-danger">
            {err.message}{' '}
            {err.code === 'interrupt_unsupported' && <button type="button" className="underline" onClick={() => void send(false)}>{V.sendWithout}</button>}
          </p>
        )}
        <p className="dash-caption text-fg-faint">{V.hint}</p>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setOpen(false)}>{V.cancel}</Button>
          <Button size="sm" disabled={busy || !text.trim()} onClick={() => void send(interrupt)}>{busy ? V.sending : V.send}</Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

const TERMINAL = new Set(['done', 'failed'])

export function RestartControl({ session, paused, onDone }: ControlProps & { onDone: (newSessionId: string | null) => void }) {
  const [open, setOpen] = useState(false)
  const [handoff, setHandoff] = useState(true)
  const [req, setReq] = useState<ControlRequest | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const inFlight = useRef(false)
  const requestId = useRef(newId())
  const settings = useAgentsRead<AgentsSettings>(open ? '/settings' : null)
  const why = blocked(paused, session.capabilities.restart, 'be restarted')
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

  return (
    <Popover open={open} onOpenChange={(o) => !running && setOpen(o)}>
      <PopoverTrigger render={<Button size="sm" variant="outline" disabled={why !== null} title={why ?? undefined} />}>{AGENTS.modal.restart}</PopoverTrigger>
      <PopoverContent align="end" className="flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2" role="dialog" aria-label={R.title}>
        <p className="dash-support text-fg">{R.explain}</p>
        <label className="flex items-center gap-2 dash-support text-fg">
          <input type="checkbox" checked={handoff} disabled={req !== null} onChange={(e) => setHandoff(e.target.checked)} />
          {R.handoff}
        </label>
        {handoff && settings.data && <p className="dash-caption text-fg-faint">{R.handoffPrompt}: {settings.data.handoff_prompt}</p>}
        {req && (
          <p role="status" className="dash-caption text-fg-muted">
            {req.state === 'waiting' ? R.waiting(minutes) : R.steps[req.state] ?? req.state}
            {req.state === 'failed' && req.step ? `. ${R.failedAt(req.step)}` : ''}
            {req.discarded_pending ? `. ${R.discarded(req.discarded_pending)}` : ''}
          </p>
        )}
        {err && <p role="alert" className="dash-caption text-danger">{err}</p>}
        <div className="flex justify-end gap-2">
          {req?.state === 'waiting' && <Button size="sm" variant="ghost" onClick={() => void skip()}>{R.skip}</Button>}
          {!running && <Button size="sm" variant="ghost" onClick={() => (setReq(null), setOpen(false))}>{R.cancel}</Button>}
          {(req === null || req.state === 'failed') && (
            <Button size="sm" disabled={running} onClick={() => (req && (requestId.current = newId(), setReq(null)), void confirm())}>{R.confirm}</Button>
          )}
          {running && <Button size="sm" disabled>{R.working}</Button>}
        </div>
      </PopoverContent>
    </Popover>
  )
}
