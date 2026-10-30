import { useCallback, useEffect, useRef, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { ArrowLeft, Copy, X } from "lucide-react"
import { MessageBody } from "../chat/MessageBody"
import { read, number, type Session, type Detail } from "./data"

type Turn = Detail["turns"][number]

/** Plain text labels, not coloured chips. */
export function roleLabel(role: string) {
  if (role === "user") return "You"
  if (role === "assistant") return "Agent"
  if (role === "tool") return "Tool"
  return role ? role[0].toUpperCase() + role.slice(1) : "Event"
}

function CopyTurn({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  // Copies only on this click; nothing is copied or logged automatically.
  const copy = () => {
    void navigator.clipboard?.writeText(text).then(() => {
      setDone(true)
      setTimeout(() => setDone(false), 1500)
    })
  }
  return (
    <Button variant="ghost" size="sm" onClick={copy} aria-label="Copy turn text">
      <Copy className="h-3 w-3" /> {done ? "Copied" : "Copy"}
    </Button>
  )
}

export function TranscriptReader({
  session,
  onClose,
}: {
  session: Session
  onClose(): void
}) {
  const [turns, setTurns] = useState<Turn[]>([])
  const [usage, setUsage] = useState<Detail["usage"]>()
  const [total, setTotal] = useState(0)
  const [next, setNext] = useState<number | undefined>(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [attempt, setAttempt] = useState(0)
  const pending = useRef<number | undefined>(0)

  // Pages of turns are appended as the reader scrolls; `pending` is the offset to fetch.
  useEffect(() => {
    if (pending.current === undefined) return
    const offset = pending.current
    const controller = new AbortController()
    setLoading(true)
    setError("")
    void read<Detail>(
      `/sessions/${session.id}?turn_offset=${offset}`,
      attempt > 0,
      controller.signal,
    )
      .then((d) => {
        if (controller.signal.aborted) return
        setTurns((t) => (offset === 0 ? d.turns : [...t, ...d.turns]))
        setUsage((u) => d.usage ?? u)
        setTotal(d.turn_total)
        const n =
          d.next_turn != null && d.next_turn > offset && d.next_turn < d.turn_total
            ? d.next_turn
            : undefined
        pending.current = n
        setNext(n)
        setLoading(false)
      })
      .catch((e) => {
        if (controller.signal.aborted) return
        setError(e instanceof Error ? e.message : "Could not load transcript.")
        setLoading(false)
      })
    return () => controller.abort()
  }, [session.id, next, attempt])

  const more = useCallback(() => {
    if (!loading && next !== undefined) pending.current = next
    setAttempt((n) => n)
  }, [loading, next])

  // Esc closes the reader (on a phone: back to the list).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      if ((e.target as HTMLElement).closest("input, textarea, select, [role=dialog], [role=listbox]")) return
      onClose()
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [onClose])

  // Load the next page when the end of the list scrolls into view.
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = end.current
    if (!el || next === undefined || typeof IntersectionObserver === "undefined") return
    const io = new IntersectionObserver((hits) => {
      if (hits.some((h) => h.isIntersecting)) more()
    })
    io.observe(el)
    return () => io.disconnect()
  }, [next, turns.length, more])

  return (
    <article
      aria-label="Transcript reader"
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto"
    >
      <header className="sticky top-0 z-10 flex items-start gap-3 border-b border-border bg-bg px-4 py-3">
        <Button variant="ghost" size="sm" className="md:hidden" onClick={onClose}>
          <ArrowLeft className="h-4 w-4" /> Sessions
        </Button>
        <div className="min-w-0 flex-1">
          <h2 className="truncate dash-body font-medium text-fg" title={session.title}>
            {session.title || "Untitled session"}
          </h2>
          <p className="truncate dash-control text-fg-muted">
            {session.tool} · {session.model || "Model unspecified"} · {session.kind}
          </p>
        </div>
        <Button variant="ghost" size="sm" className="max-md:hidden" onClick={onClose} aria-label="Close reader">
          <X className="h-4 w-4" />
        </Button>
      </header>
      <dl className="grid gap-3 border-b border-border px-4 py-3 dash-control sm:grid-cols-2">
        <div>
          <dt className="text-fg-faint">Project</dt>
          <dd className="mt-1 break-all text-fg-muted">{session.project || "Unspecified"}</dd>
        </div>
        <div>
          <dt className="text-fg-faint">Started</dt>
          <dd className="mt-1 text-fg-muted">
            {session.started_at ? new Date(session.started_at).toLocaleString() : "Unknown"}
          </dd>
        </div>
        <div>
          <dt className="text-fg-faint">Recorded activity</dt>
          <dd className="mt-1 text-fg-muted">
            {number(session.turns)} turns · {number(session.tool_calls)} tool calls · {number(session.errors)} tool errors
          </dd>
        </div>
        <div>
          <dt className="text-fg-faint">Session ID</dt>
          <dd className="mt-1 break-all font-mono dash-caption text-fg-muted">{session.id}</dd>
        </div>
        {usage && (
          <div className="sm:col-span-2">
            <dt className="text-fg-faint">Tokens</dt>
            <dd className="mt-1 text-fg-muted">
              {number(usage.input_tokens)} in · {number(usage.output_tokens)} out · {number(usage.cache_read_tokens)} cache reads · {number(usage.cache_creation_tokens)} cache writes
            </dd>
          </div>
        )}
      </dl>
      <div className="px-4 py-2 dash-control text-fg-faint">
        {turns.length
          ? `${number(turns.length)} of ${number(total)} turns · redacted transcript`
          : loading || error
            ? ""
            : "No recorded turns"}
      </div>
      <div className="divide-y divide-border-subtle px-4">
        {turns.map((turn) => (
          <article
            key={turn.seq}
            className="py-4 [contain-intrinsic-size:auto_120px] [content-visibility:auto]"
          >
            <div className="mb-2 flex items-center gap-3 dash-control">
              <span className="font-medium text-fg-secondary">{roleLabel(turn.role)}</span>
              <span className="text-fg-faint">#{turn.seq}</span>
              {turn.ts && (
                <time className="text-fg-faint" title={new Date(turn.ts).toLocaleString()}>
                  {new Date(turn.ts).toLocaleTimeString()}
                </time>
              )}
              {turn.text && (
                <span className="ml-auto">
                  <CopyTurn text={turn.text} />
                </span>
              )}
            </div>
            {turn.text ? (
              <MessageBody body={turn.text} />
            ) : (
              <p className="dash-control italic text-fg-faint">No text recorded for this event.</p>
            )}
          </article>
        ))}
      </div>
      {error && (
        <p role="alert" className="px-4 py-3 dash-control text-danger">
          {error}{" "}
          <Button variant="ghost" size="sm" onClick={() => { pending.current = next ?? 0; setAttempt((n) => n + 1) }}>
            Retry
          </Button>
        </p>
      )}
      {loading && !error && (
        <p role="status" className="px-4 py-6 dash-body text-fg-faint">
          Loading transcript…
        </p>
      )}
      <div ref={end} className="px-4 py-3">
        {next !== undefined && !loading && !error && (
          <Button variant="outline" size="sm" onClick={more}>
            Load more turns
          </Button>
        )}
      </div>
    </article>
  )
}
