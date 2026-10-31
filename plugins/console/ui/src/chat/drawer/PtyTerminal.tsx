import { useEffect, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'

export function sessionURL(wsPath: string, loc: Pick<Location, 'protocol' | 'host'> = window.location) {
  return `${loc.protocol === 'https:' ? 'wss:' : 'ws:'}//${loc.host}${wsPath}`
}

/**
 * One quick-terminal session (PTY bytes both ways, resize as a text frame, `state`/`exit` control frames).
 * Typed input goes to the process only: nothing typed is kept in state, storage or logs. State `stale` (the operator
 * elevation lapsed) makes the pane read-only.
 */
export function PtyTerminal({ wsPath, onState }: { wsPath: string; onState?: (s: string) => void }) {
  const host = useRef<HTMLDivElement>(null)
  const cb = useRef(onState)
  cb.current = onState
  useEffect(() => {
    if (!host.current) return
    const t = new Terminal({ cursorBlink: true, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12, scrollback: 5000, allowProposedApi: false })
    const fit = new FitAddon()
    t.loadAddon(fit)
    t.open(host.current)
    try { fit.fit() } catch { /* not laid out yet */ }
    const ws = new WebSocket(sessionURL(wsPath))
    ws.binaryType = 'arraybuffer'
    let readOnly = false
    const size = () => { if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols: t.cols, rows: t.rows })) }
    ws.onopen = () => { cb.current?.('connected'); size() }
    ws.onmessage = (e) => {
      if (typeof e.data !== 'string') { t.write(new Uint8Array(e.data as ArrayBuffer)); return }
      try {
        const f = JSON.parse(e.data) as { type: string; state?: string; code?: number }
        if (f.type === 'state' && f.state) { readOnly = f.state !== 'open'; cb.current?.(f.state) }
        else if (f.type === 'exit') { readOnly = true; cb.current?.(`exited (${f.code ?? 0})`) }
      } catch { /* ignore malformed control frames */ }
    }
    ws.onclose = () => { readOnly = true; cb.current?.('disconnected') }
    const data = t.onData((d) => { if (!readOnly && ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(d)) })
    const resize = t.onResize(size)
    const ro = new ResizeObserver(() => { try { fit.fit() } catch { /* hidden */ } })
    ro.observe(host.current)
    return () => { ro.disconnect(); data.dispose(); resize.dispose(); ws.close(); t.dispose() }
  }, [wsPath])
  return <div ref={host} role="group" aria-label="Terminal session" className="min-h-0 flex-1 overflow-hidden rounded-control border border-border-subtle bg-bg-elevated p-1" />
}
