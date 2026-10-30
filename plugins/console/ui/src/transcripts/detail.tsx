import { useCallback, useEffect, useRef, useState } from "react"
import { Button, Input, Skeleton } from "@hollis-labs/design-components"
import { ArrowLeft, ChevronDown, ChevronUp, Copy, X } from "lucide-react"
import { Segmented } from "../components/segmented"
import { MessageBody } from "../chat/MessageBody"
import { normalizeSession, read, number, size, took, TranscriptError, type Session, type Detail } from "./data"
import { ago } from "./sessions"

type Turn = Detail["turns"][number]

/** Plain text labels, not coloured chips. */
export function roleLabel(role: string) {
  if (role === "user") return "You"
  if (role === "assistant") return "Agent"
  if (role === "tool") return "Tool"
  return role ? role[0].toUpperCase() + role.slice(1) : "Event"
}

function CopyTurn({ text, label = "Copy turn text" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  // Copies only on this click; nothing is copied or logged automatically.
  const copy = () => {
    void navigator.clipboard?.writeText(text).then(() => {
      setDone(true)
      setTimeout(() => setDone(false), 1500)
    })
  }
  return (
    <Button variant="ghost" size="sm" onClick={copy} aria-label={label}>
      <Copy className="h-3 w-3" /> {done ? "Copied" : label === "Copy turn text" ? "Copy" : label}
    </Button>
  )
}

const FRIENDLY: Record<string, string> = {
  Bash: "Ran shell",
  Read: "Read file",
  Edit: "Edited file",
  MultiEdit: "Edited file",
  Write: "Wrote file",
  Grep: "Searched files",
  Glob: "Listed files",
  WebFetch: "Fetched page",
  WebSearch: "Searched the web",
}

function ToolCalls({
  seq,
  calls,
}: {
  seq: number
  calls: NonNullable<Detail["turns"][number]["tool_calls"]>
}) {
  const [open, setOpen] = useState<number>(-1)
  const [all, setAll] = useState(false)
  const anyFailed = calls.some((c) => c.status === "error")
  // More than three calls fold under a summary, unless one failed (so Next error can land on it).
  if (calls.length > 3 && !all && !anyFailed)
    return (
      <button
        type="button"
        aria-expanded={false}
        onClick={() => setAll(true)}
        className="mt-2 dash-control text-fg-muted underline"
      >
        {calls.length} tool calls
      </button>
    )
  return (
    <ul className="mt-2 space-y-1 dash-control text-fg-muted" aria-label="Tool calls">
      {calls.map((c, i) => (
        <li key={i} id={`call-${seq}-${i}`} tabIndex={-1} className="outline-none focus-visible:bg-surface">
          {FRIENDLY[c.name] ? <span>{FRIENDLY[c.name]}</span> : <span className="font-mono dash-caption">{c.name}</span>}
          {c.target && <span className="break-all"> {c.target}</span>}
          {c.status === "error" && (
            <span className="text-danger">
              {" "}· <X className="inline h-3 w-3" aria-hidden /> failed
            </span>
          )}
          {c.result_bytes != null && <span className="text-fg-faint"> · {size(c.result_bytes)}</span>}
          {took(c) && <span className="text-fg-faint"> · {took(c)}</span>}
          {c.error_excerpt && (
            <>
              {" "}
              <button className="underline" onClick={() => setOpen(open === i ? -1 : i)}>
                {open === i ? "Hide error" : "Show error"}
              </button>
              {open === i && (
                <div className="mt-1">
                  <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-border-subtle p-2 dash-caption text-fg-secondary">
                    {c.error_excerpt}
                  </pre>
                  <CopyTurn text={c.error_excerpt} label="Copy error" />
                </div>
              )}
            </>
          )}
        </li>
      ))}
    </ul>
  )
}

export function TranscriptReader({
  session: initial,
  onClose,
}: {
  session: Session
  onClose(): void
}) {
  // A deep-linked id outside the snapshot starts as a stub; the page response fills it in.
  const [session, setSession] = useState(initial)
  const [missing, setMissing] = useState(false)
  const [errCur, setErrCur] = useState(0)
  const [last, setLast] = useState<"find" | "error">("find")
  const [turns, setTurns] = useState<Turn[]>([])
  const [usage, setUsage] = useState<Detail["usage"]>()
  const [total, setTotal] = useState(0)
  const [next, setNext] = useState<number | undefined>(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [attempt, setAttempt] = useState(0)
  const pending = useRef<number | undefined>(0)
  const [find, setFind] = useState("")
  const [role, setRole] = useState<"all" | "user" | "assistant" | "tool">("all")
  const [cur, setCur] = useState(0)

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
        if (d.session) setSession(normalizeSession(d.session as unknown as Record<string, unknown>))
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
        // Unreadable and unknown ids read the same, so existence is not revealed.
        if (e instanceof TranscriptError && (e.code === "not_found" || e.status === 403 || e.status === 404)) setMissing(true)
        else setError(e instanceof Error ? e.message : "Could not load transcript.")
        setLoading(false)
      })
    return () => controller.abort()
  }, [session.id, next, attempt])

  const more = useCallback(() => {
    if (!loading && next !== undefined) pending.current = next
    setAttempt((n) => n)
  }, [loading, next])

  // Find works on the turns loaded so far; the server has no find.
  const shown = turns.filter((t) => role === "all" || t.role === role)
  const q = find.trim().toLowerCase()
  const hits = q
    ? shown.filter((t) => (t.text ?? "").toLowerCase().includes(q)).map((t) => t.seq)
    : []
  const at = hits.length ? Math.min(cur, hits.length - 1) : -1
  const current = at >= 0 ? hits[at] : undefined
  useEffect(() => {
    if (current === undefined) return
    document.getElementById(`turn-${current}`)?.scrollIntoView?.({ block: "center" })
  }, [current])
  const step = (by: number) => {
    if (!hits.length) return
    setLast("find")
    setCur((at + by + hits.length) % hits.length)
  }
  // Failed tool calls in the loaded turns; Next error steps through them.
  const bad = shown.flatMap((t) =>
    (t.tool_calls ?? []).flatMap((c, i) => (c.status === "error" ? [{ seq: t.seq, i }] : [])),
  )
  const eAt = bad.length ? Math.min(errCur, bad.length - 1) : -1
  const nextError = (by = 1) => {
    if (!bad.length) return
    const to = last === "error" ? (eAt + by + bad.length) % bad.length : by > 0 ? 0 : bad.length - 1
    setLast("error")
    setErrCur(to)
    const row = document.getElementById(`call-${bad[to].seq}-${bad[to].i}`)
    row?.scrollIntoView?.({ block: "center" })
    row?.focus()
  }
  // E / Shift+E jump between failed calls while not typing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "e" || e.metaKey || e.ctrlKey || e.altKey) return
      if ((e.target as HTMLElement).closest("input, textarea, select, [contenteditable], [role=dialog]")) return
      if (!bad.length) return
      e.preventDefault()
      nextError(e.shiftKey ? -1 : 1)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  })

  // Ctrl/Cmd+F finds in the transcript only while focus is inside the reader.
  const box = useRef<HTMLElement>(null)
  const onKeyDown = (e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "f") {
      e.preventDefault()
      box.current?.querySelector<HTMLInputElement>("input[aria-label='Find in transcript']")?.focus()
    }
  }

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
      ref={box}
      onKeyDown={onKeyDown}
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
          <dd className="mt-1 text-fg-muted" title={session.started_at ? new Date(session.started_at).toLocaleString() : undefined}>
            {session.started_at ? ago(session.started_at) : "Unknown"}
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
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <Input
          type="search"
          value={find}
          onChange={(e) => {
            setFind(e.target.value)
            setCur(0)
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault()
              step(e.shiftKey ? -1 : 1)
            }
          }}
          placeholder="Find in transcript"
          aria-label="Find in transcript"
          className="h-8 w-56 dash-body"
        />
        <Button variant="ghost" size="sm" aria-label="Previous match" disabled={!hits.length} onClick={() => step(-1)}>
          <ChevronUp className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="sm" aria-label="Next match" disabled={!hits.length} onClick={() => step(1)}>
          <ChevronDown className="h-4 w-4" />
        </Button>
        {bad.length > 0 && (
          <Button variant="outline" size="sm" onClick={() => nextError(1)}>
            Next error
          </Button>
        )}
        <span role="status" aria-live="polite" className="dash-control text-fg-faint">
          {last === "error" && bad.length
            ? `${eAt + 1} of ${bad.length} failed calls${next !== undefined ? " in loaded turns" : ""}`
            : q
            ? hits.length
              ? `${at + 1} of ${hits.length}${next !== undefined ? " in loaded turns" : ""}`
              : next !== undefined
                ? "No matches in loaded turns"
                : "No matches"
            : ""}
        </span>
        <Segmented
          bare
          label="Filter by speaker"
          value={role}
          onChange={setRole}
          className="ml-auto"
          items={[
            { value: "all", label: "All" },
            { value: "user", label: "You" },
            { value: "assistant", label: "Agent" },
            { value: "tool", label: "Tool" },
          ]}
        />
      </div>
      <div className="px-4 py-2 dash-control text-fg-faint">
        {turns.length
          ? `${role === "all" ? `${number(turns.length)} of ${number(total)} turns` : `${number(shown.length)} ${roleLabel(role).toLowerCase()} turns of ${number(turns.length)} loaded`} · redacted transcript`
          : loading || error
            ? ""
            : "No recorded turns"}
      </div>
      <div className="divide-y divide-border-subtle px-4">
        {shown.map((turn) => (
          <article
            key={turn.seq}
            id={`turn-${turn.seq}`}
            aria-current={turn.seq === current || (last === "error" && turn.seq === bad[eAt]?.seq) ? "true" : undefined}
            className={`py-4 [contain-intrinsic-size:auto_120px] [content-visibility:auto] ${turn.seq === current || (last === "error" && turn.seq === bad[eAt]?.seq) ? "bg-surface" : ""}`}
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
            {!!turn.tool_calls?.length && <ToolCalls seq={turn.seq} calls={turn.tool_calls} />}
          </article>
        ))}
      </div>
      {missing && (
        <div className="space-y-3 px-4 py-10 text-center dash-body text-fg-faint">
          <p>This session was not found.</p>
          <Button variant="outline" size="sm" onClick={onClose}>
            Back to sessions
          </Button>
        </div>
      )}
      {error && (
        <p role="alert" className="px-4 py-3 dash-control text-danger">
          {error}{" "}
          <Button variant="ghost" size="sm" onClick={() => { pending.current = next ?? 0; setAttempt((n) => n + 1) }}>
            Retry
          </Button>
        </p>
      )}
      {loading && !error && !missing && (
        <div role="status" className="space-y-3 px-4 py-4">
          <span className="sr-only">Loading transcript…</span>
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      )}
      <div ref={end} className="px-4 py-3">
        {next !== undefined && !loading && !error && !missing && (
          <Button variant="outline" size="sm" onClick={more}>
            Load more turns
          </Button>
        )}
      </div>
    </article>
  )
}
