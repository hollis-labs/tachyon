import { useCallback, useEffect, useRef, useState } from "react"
import { Button, OverflowMenu, SearchInput, Skeleton } from "@hollis-labs/design-components"
import { ArrowLeft, ChevronDown, ChevronUp, Copy } from "lucide-react"
import { Segmented } from "../components/segmented"
import { MessageBody } from "../chat/MessageBody"
import { normalizeSession, projectName, read, number, size, took, TranscriptError, type Session, type Detail } from "./data"

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

function ToolCalls({
  seq,
  calls,
  missing,
}: {
  seq: number
  calls: NonNullable<Detail["turns"][number]["tool_calls"]>
  /** Sources without recorded calls (ChatGPT exports) say so instead of showing nothing. */
  missing?: boolean
}) {
  const [open, setOpen] = useState<number>(-1)
  if (!calls.length) return missing ? <p className="mt-2 dash-support text-fg-faint">No tool calls recorded</p> : null
  return (
    <ul className="mt-2 space-y-1.5" aria-label="Tool calls">
      {calls.map((c, i) => {
        const bad = c.status === "error"
        const expandable = !!c.error_excerpt
        const status = bad ? `failed${took(c) ? ` · ${took(c)}` : ""}` : [c.status === "ok" ? "ok" : "", took(c), c.result_bytes != null ? size(c.result_bytes) : ""].filter(Boolean).join(" ")
        const Row = expandable ? "button" : "div"
        return (
          <li key={i} id={`call-${seq}-${i}`} tabIndex={-1} className="outline-none focus-visible:bg-surface">
            <Row
              {...(expandable ? { type: "button" as const, "aria-expanded": open === i, "aria-label": open === i ? "Hide error" : "Show error", onClick: () => setOpen(open === i ? -1 : i) } : {})}
              className={`flex w-full items-center gap-2 rounded-control border px-3 py-1.5 text-left dash-support ${bad ? "border-danger/60" : "border-border"}`}
            >
              <span aria-hidden className={`w-3 shrink-0 text-center dash-caption ${bad ? "text-danger" : "text-fg-muted"}`}>{bad ? "✗" : expandable ? (open === i ? "▾" : "▸") : "▸"}</span>
              <span className="shrink-0 font-semibold text-fg">{c.name}</span>
              {c.target && <span className="min-w-0 flex-1 truncate text-fg-secondary">{c.target}</span>}
              {!c.target && <span className="flex-1" />}
              {status && <span className={`shrink-0 font-mono dash-caption ${bad ? "text-danger" : "text-fg-muted"}`}>{status}</span>}
            </Row>
            {open === i && c.error_excerpt && (
              <div className="mt-1.5 rounded-control border border-border px-3 py-2">
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono dash-caption text-fg-secondary">{c.error_excerpt}</pre>
                <CopyTurn text={c.error_excerpt} label="Copy error" />
              </div>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** Single-letter disc for the rail; the user gets a person mark, agents their tool initial. */
function Mark({ role, tool }: { role: string; tool: string }) {
  const user = role === "user"
  return <span aria-hidden className="relative z-10 flex size-6 shrink-0 items-center justify-center rounded-full border border-border bg-bg font-mono dash-caption font-semibold uppercase text-fg-secondary">{user ? "U" : (tool || role || "?")[0]}</span>
}

const clock = (ts?: string) => (ts ? new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }) : "")

/** Large figures in the usage strip: 1.1M, 0.3M. */
const fig = (n = 0) => new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(n)

function CopyButton({ text, label, done }: { text: string; label: string; done: string }) {
  const [ok, setOk] = useState(false)
  return (
    <Button variant="outline" size="sm" onClick={() => void navigator.clipboard?.writeText(text).then(() => { setOk(true); setTimeout(() => setOk(false), 1500) })}>
      {ok ? done : label}
    </Button>
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
  // Matches are painted with the CSS Custom Highlight API: plain text ranges, never markup.
  useEffect(() => {
    const reg = (globalThis as { CSS?: { highlights?: Map<string, unknown> } }).CSS?.highlights
    const Hl = (globalThis as { Highlight?: new (...r: Range[]) => unknown }).Highlight
    if (!reg || !Hl) return
    if (!q) {
      reg.delete("transcript-find")
      return
    }
    const ranges: Range[] = []
    for (const el of box.current?.querySelectorAll("[data-turn-text]") ?? []) {
      const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const text = (n.textContent ?? "").toLowerCase()
        for (let i = text.indexOf(q); i >= 0; i = text.indexOf(q, i + q.length)) {
          const r = document.createRange()
          r.setStart(n, i)
          r.setEnd(n, i + q.length)
          ranges.push(r)
        }
      }
    }
    reg.set("transcript-find", new Hl(...ranges))
    return () => {
      reg.delete("transcript-find")
    }
  }, [q, shown.length, turns.length])
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
      box.current?.querySelector<HTMLInputElement>("input[aria-label='Find in loaded turns']")?.focus()
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

  const gaps = new Set<number>()
  let prev = 0
  for (const t of shown) {
    const at = t.ts ? Date.parse(t.ts) : 0
    if (at && (!prev || at - prev > 600000)) gaps.add(t.seq)
    if (at) prev = at
  }
  const link = typeof window === "undefined" ? "" : `${window.location.origin}/dashboards/transcripts?session=${session.id}`
  return (
    <article
      ref={box}
      onKeyDown={onKeyDown}
      aria-label="Transcript reader"
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto"
    >
      <style>{"::highlight(transcript-find){background-color:var(--color-selection);color:inherit}"}</style>
      <button type="button" onClick={onClose} className="flex shrink-0 items-center gap-1 border-b border-border px-4 py-2.5 text-left dash-label text-fg-secondary md:hidden">
        <ArrowLeft className="size-3.5" aria-hidden /> Sessions
      </button>
      <header className="px-5 pb-3 pt-4 max-md:px-4">
        <div className="flex items-start gap-3">
          <div className="min-w-0 flex-1">
            <h2 className="font-sans text-lg font-semibold normal-case leading-snug tracking-normal text-fg" title={session.title}>
              {session.title || "Untitled session"}
            </h2>
            <p className="mt-0.5 dash-support text-fg-muted">
              {[session.tool, session.model || "Model unspecified", `${session.kind === "subagent" ? "subagent" : "main"} session`].join(" · ")}
              {session.started_at && ` · started ${new Date(session.started_at).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}`}
              {session.project && <> · <span className="font-mono">{projectName(session.project)}</span></>}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <CopyButton text={session.id} label="Copy ID" done="Copied" />
            <CopyButton text={link} label="Copy link" done="Copied" />
            <OverflowMenu ariaLabel="More" actions={[{ label: "Close", onSelect: onClose }]} />
          </div>
        </div>
        <p className="mt-3 flex flex-wrap items-baseline gap-x-4 gap-y-1 dash-support text-fg-muted" aria-label="Session usage">
          <span><b className="font-mono font-normal text-fg">{number(session.turns)}</b> turns</span>
          <span><b className="font-mono font-normal text-fg">{number(session.tool_calls)}</b> tool calls</span>
          <span className={session.errors ? "text-danger" : ""}><b className="font-mono font-normal">{number(session.errors)}</b> tool errors</span>
          {usage && (
            <>
              <span aria-hidden className="h-3 border-l border-border" />
              <span>in <b className="font-mono font-normal text-fg">{fig(usage.input_tokens)}</b></span>
              <span>out <b className="font-mono font-normal text-fg">{fig(usage.output_tokens)}</b></span>
              <span>cache read <b className="font-mono font-normal text-fg">{fig(usage.cache_read_tokens)}</b></span>
              <span>cache write <b className="font-mono font-normal text-fg">{fig(usage.cache_creation_tokens)}</b></span>
            </>
          )}
        </p>
      </header>
      <div className="flex flex-wrap items-center gap-2 border-y border-border px-5 py-2.5 max-md:px-4">
        <div className="flex min-w-0 basis-72 items-center gap-1.5 max-md:basis-full md:max-w-md md:flex-1">
          <div className="dash-search-adapter min-w-0 flex-1">
            <SearchInput
              ariaLabel="Find in loaded turns"
              placeholder={next !== undefined ? "Find in loaded turns" : "Find in transcript"}
              value={find}
              onChange={(v) => {
                setFind(v)
                setCur(0)
              }}
              debounceMs={0}
            />
          </div>
          {q && (
            <>
              <Button variant="outline" size="icon-sm" aria-label="Previous match" disabled={!hits.length} onClick={() => step(-1)}>
                <ChevronUp className="size-4" />
              </Button>
              <Button variant="outline" size="icon-sm" aria-label="Next match" disabled={!hits.length} onClick={() => step(1)}>
                <ChevronDown className="size-4" />
              </Button>
            </>
          )}
        <span role="status" aria-live="polite" className={`dash-label text-fg-muted`}>
          {last === "error" && bad.length
            ? `${eAt + 1} of ${bad.length} failed calls${next !== undefined ? " in loaded turns" : ""}`
            : q
            ? hits.length
              ? `${at + 1} of ${hits.length}${next !== undefined ? " in loaded turns" : ""}`
              : next !== undefined
                ? "No matches in loaded turns · Load more to search further"
                : "No matches"
            : ""}
        </span>
        </div>
        <Segmented
          bare
          label="Filter by speaker"
          value={role}
          onChange={setRole}
          items={[
            { value: "all", label: "All" },
            { value: "user", label: "You" },
            { value: "assistant", label: "Agent" },
            { value: "tool", label: "Tool" },
          ]}
        />
        {bad.length > 0 && (
          <Button variant="outline" size="sm" className="rounded-full" onClick={() => nextError(1)}>
            Next error ↓ {bad.length}
          </Button>
        )}
      </div>
      {!turns.length && !loading && !error && !missing && <p className="px-5 py-3 dash-support text-fg-faint">No recorded turns</p>}
      <div className="relative px-5 py-4 max-md:px-4">
        {shown.length > 0 && <span aria-hidden className="absolute bottom-0 left-8 top-0 border-l border-border max-md:left-7" />}
        {shown.map((turn) => {
          const active = turn.seq === current || (last === "error" && turn.seq === bad[eAt]?.seq)
          return (
            <div key={turn.seq}>
              {gaps.has(turn.seq) && (
                <div className="relative z-10 mb-3 ml-9 mt-2"><span className="inline-block rounded-full bg-surface px-2.5 py-0.5 font-mono dash-label text-fg-secondary">{clock(turn.ts)}</span></div>
              )}
              <article
                id={`turn-${turn.seq}`}
                aria-current={active ? "true" : undefined}
                className={`group relative mb-4 flex gap-3 [contain-intrinsic-size:auto_120px] [content-visibility:auto] ${active ? "rounded-control bg-surface" : ""}`}
              >
                <Mark role={turn.role} tool={session.tool} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline gap-2 dash-support">
                    <span className="font-medium text-fg">{turn.role === "user" ? "You" : session.tool || roleLabel(turn.role)}</span>
                    <span className="font-mono dash-caption text-fg-muted">#{turn.seq}</span>
                    {turn.ts && (
                      <time className="font-mono dash-caption text-fg-muted" title={new Date(turn.ts).toLocaleString()}>
                        {clock(turn.ts)}
                      </time>
                    )}
                    {turn.text && (
                      <span className="ml-auto opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 max-md:opacity-100">
                        <CopyTurn text={turn.text} />
                      </span>
                    )}
                  </div>
                  {turn.text ? (
                    <div className="mt-0.5" data-turn-text><MessageBody body={turn.text} /></div>
                  ) : (
                    <p className="mt-0.5 dash-support italic text-fg-faint">No text recorded for this event.</p>
                  )}
                  {turn.truncated && <p className="mt-1 dash-support text-fg-faint">This turn was cut off at 20 KiB.</p>}
                  {!!turn.thinking_bytes && <p className="mt-1 dash-support text-fg-faint">Thinking ({number(turn.thinking_bytes)} bytes)</p>}
                  {!!turn.tool_calls?.length && <ToolCalls seq={turn.seq} calls={turn.tool_calls} />}
                </div>
              </article>
            </div>
          )
        })}
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
