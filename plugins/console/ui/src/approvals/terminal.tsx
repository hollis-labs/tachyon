import { useCallback, useEffect, useRef, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { FitAddon } from "@xterm/addon-fit"
import { Terminal } from "@xterm/xterm"
import "@xterm/xterm/css/xterm.css"
import { ACTION_RUNS_BASE, type ActionRun } from "./client"
import { operatorFetch } from "../operator/elevation"

const label = "font-mono dash-caption uppercase tracking-widest text-fg-faint"

export const RUN_STATE_COPY: Record<ActionRun["state"], string> = {
  pending_approval: "Waiting for your approval",
  approved: "Approved, starting",
  waiting_input: "Waiting for you: open the terminal and answer the prompts",
  running: "Running",
  succeeded: "Succeeded",
  failed: "Failed",
  refused: "Refused: the request that would run is not the request that was approved",
  denied: "Denied",
  expired: "Expired before a decision",
}

export function terminalURL(runId: string, loc: Pick<Location, "protocol" | "host"> = window.location) {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:"
  return `${scheme}//${loc.host}${ACTION_RUNS_BASE}/${encodeURIComponent(runId)}/terminal`
}

function elapsed(run: ActionRun, now: number) {
  if (!run.started_at) return ""
  const end = run.ended_at ? Date.parse(run.ended_at) : now
  const s = Math.max(0, Math.round((end - Date.parse(run.started_at)) / 1000))
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`
}

// LiveTerminal attaches to one existing run. Keystrokes go to the process as
// binary frames; nothing typed is kept in React state, storage or logs.
export function LiveTerminal({ run, onExit }: { run: ActionRun; onExit(): void }) {
  const host = useRef<HTMLDivElement>(null)
  const term = useRef<Terminal | null>(null)
  const [status, setStatus] = useState("connecting")
  useEffect(() => {
    if (!host.current) return
    const t = new Terminal({
      convertEol: false,
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 13,
      scrollback: 5000,
      allowProposedApi: false,
    })
    const fit = new FitAddon()
    t.loadAddon(fit)
    t.open(host.current)
    term.current = t
    try {
      fit.fit()
    } catch { /* not laid out yet */ }
    const ws = new WebSocket(terminalURL(run.run_id))
    ws.binaryType = "arraybuffer"
    const sendSize = () => {
      if (ws.readyState === WebSocket.OPEN)
        ws.send(JSON.stringify({ type: "resize", cols: t.cols, rows: t.rows }))
    }
    ws.onopen = () => {
      setStatus("connected")
      sendSize()
    }
    ws.onmessage = (event) => {
      if (typeof event.data !== "string") {
        t.write(new Uint8Array(event.data as ArrayBuffer))
        return
      }
      try {
        const ev = JSON.parse(event.data) as { type: string; state?: string; code?: number; error?: string }
        if (ev.type === "exit") {
          setStatus(ev.state ?? "ended")
          onExit()
        } else if (ev.type === "state" && ev.state) {
          setStatus(ev.state)
        }
      } catch { /* ignore malformed control frames */ }
    }
    ws.onclose = () => setStatus((s) => (s === "connected" || s === "connecting" ? "disconnected" : s))
    const data = t.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(d))
    })
    const resize = t.onResize(sendSize)
    const observer = new ResizeObserver(() => {
      try {
        fit.fit()
      } catch { /* hidden */ }
    })
    observer.observe(host.current)
    return () => {
      observer.disconnect()
      data.dispose()
      resize.dispose()
      ws.close()
      t.dispose()
      term.current = null
    }
  }, [run.run_id, onExit])
  const copy = useCallback(() => {
    const t = term.current
    if (!t) return
    t.selectAll()
    const text = t.getSelection()
    t.clearSelection()
    void navigator.clipboard?.writeText(text)
  }, [])
  return (
    <div>
      <div
        ref={host}
        role="group"
        aria-label={`Terminal for ${run.command}`}
        className="h-72 w-full overflow-hidden rounded border border-border bg-bg p-1"
      />
      <div className="mt-2 flex items-center justify-between dash-support text-fg-faint">
        <span role="status">Terminal: {status}. Typed input is sent to the process and is not recorded.</span>
        <Button variant="outline" size="sm" className="font-mono dash-label" onClick={copy}>
          Copy output
        </Button>
      </div>
    </div>
  )
}

// ActionRunPanel shows machine, exact command, digest and state for an
// approval's registered action, and the live terminal once it can be opened.
export function ActionRunPanel({
  initial,
  approved,
  fetcher = operatorFetch,
  pollMs = 1500,
}: {
  initial: ActionRun
  approved: boolean
  fetcher?: typeof fetch
  pollMs?: number
}) {
  const [run, setRun] = useState(initial)
  const [open, setOpen] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const [error, setError] = useState("")
  useEffect(() => setRun(initial), [initial])
  const reload = useCallback(async () => {
    try {
      const res = await fetcher(`${ACTION_RUNS_BASE}/${encodeURIComponent(initial.run_id)}`, {
        headers: { Accept: "application/json", "Cache-Control": "no-cache" },
      })
      if (!res.ok) throw new Error("Could not read the action run.")
      setRun((await res.json()) as ActionRun)
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not read the action run.")
    }
  }, [fetcher, initial.run_id])
  const active = approved && !run.terminal
  useEffect(() => {
    if (!active) return
    void reload()
    const id = window.setInterval(() => {
      void reload()
      setNow(Date.now())
    }, pollMs)
    return () => window.clearInterval(id)
  }, [active, reload, pollMs])
  // Interactive runs open their terminal as soon as they wait for the owner.
  useEffect(() => {
    if (run.state === "waiting_input" || run.state === "running") setOpen(true)
  }, [run.state])
  const onExit = useCallback(() => void reload(), [reload])
  const tone = run.state === "succeeded" ? "text-success" : run.terminal ? "text-danger" : "text-warning"
  return (
    <section aria-label="Action run" className="rounded border border-border bg-surface/40 p-4">
      <h3 className={label}>Runs on {run.machine}</h3>
      <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-all rounded bg-bg p-2 font-mono dash-support text-fg">{run.command}</pre>
      <dl className="mt-3 grid grid-cols-2 gap-3 dash-support text-fg-muted">
        <div>
          <dt className={label}>Action</dt>
          <dd className="font-mono">{run.action}</dd>
        </div>
        <div>
          <dt className={label}>Class</dt>
          <dd>{run.class}{run.labels ? ` · ${run.labels}` : ""}</dd>
        </div>
        <div className="col-span-2">
          <dt className={label}>op_digest</dt>
          <dd className="break-all font-mono">{run.op_digest}</dd>
        </div>
      </dl>
      <p role="status" className={`mt-3 font-mono dash-caption uppercase tracking-widest ${tone}`}>
        {RUN_STATE_COPY[run.state]}
        {run.exit_code !== undefined ? ` · exit ${run.exit_code}` : ""}
        {run.started_at ? ` · ${elapsed(run, now)}` : ""}
      </p>
      {run.error && <p className="mt-1 dash-support text-danger">{run.error}</p>}
      {run.terminal && run.note && <p className="mt-1 dash-support text-fg-muted">{run.note}</p>}
      {error && <p role="alert" className="mt-1 dash-support text-danger">{error}</p>}
      {open && !run.terminal && <div className="mt-3"><LiveTerminal run={run} onExit={onExit} /></div>}
      {run.terminal && run.output && (
        <details className="mt-3" open={run.state !== "succeeded"}>
          <summary className="cursor-pointer dash-control text-fg-muted">Recorded output (input is never recorded)</summary>
          <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-bg p-2 font-mono dash-support text-fg-muted">{run.output}</pre>
        </details>
      )}
      {!run.terminal && approved && !open && run.state !== "pending_approval" && (
        <Button variant="outline" size="sm" className="mt-3 font-mono dash-label" onClick={() => setOpen(true)}>
          Open terminal
        </Button>
      )}
    </section>
  )
}
