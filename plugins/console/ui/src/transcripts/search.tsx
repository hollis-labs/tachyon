import { Fragment, useEffect, useState } from "react"
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@hollis-labs/design-components"
import { ago } from "./sessions"
import { normalizeSession, projectName, read, type Filters, type Session } from "./data"

export interface Status {
  modes?: string[]
  capabilities?: { search?: boolean }
}

interface Match {
  snippet?: string
  highlight?: { format?: string; ranges?: { start: number; end: number }[] }
}
interface Hit {
  session: Session
  match?: Match
}
interface SearchResult {
  items: Record<string, unknown>[]
  next_cursor?: string | null
  notice?: { message?: string }
}

/** Content search is offered only when the bridge says so; an absent flag means no. */
export function useSearchStatus() {
  const [status, setStatus] = useState<Status>()
  useEffect(() => {
    const c = new AbortController()
    void read<Status>("/status", false, c.signal)
      .then((s) => !c.signal.aborted && setStatus(s))
      .catch(() => {})
    return () => c.abort()
  }, [])
  return status
}

/** Snippet text with the matched span in bold; plain text only, never markup. */
export function Highlighted({ snippet = "", highlight }: Match & { snippet?: string }) {
  const spans: [number, number][] = []
  if (highlight?.format === "ranges" && highlight.ranges)
    for (const r of highlight.ranges) spans.push([r.start, r.end])
  else
    for (const m of snippet.matchAll(/\[([^\]]+)\]/g))
      spans.push([m.index!, m.index! + m[0].length])
  if (!spans.length) return <>{snippet}</>
  const bracket = highlight?.format !== "ranges"
  const out: React.ReactNode[] = []
  let at = 0
  spans.forEach(([a, b], i) => {
    out.push(<Fragment key={`t${i}`}>{snippet.slice(at, a)}</Fragment>)
    out.push(<strong key={`m${i}`} className="font-semibold text-fg">{bracket ? snippet.slice(a + 1, b - 1) : snippet.slice(a, b)}</strong>)
    at = b
  })
  out.push(<Fragment key="end">{snippet.slice(at)}</Fragment>)
  return <>{out}</>
}

export function ContentSearch({
  filter,
  modes,
  activeId,
  onOpen,
  onActive,
}: {
  filter: Filters
  modes: string[]
  activeId: string
  onOpen(s: Session): void
  onActive(id: string): void
}) {
  // The query lives in component state only: not in the URL, history or logs.
  const [q, setQ] = useState("")
  const [mode, setMode] = useState("text")
  const [hits, setHits] = useState<Hit[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [notice, setNotice] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [asked, setAsked] = useState("")

  const run = async (query: string, after?: string) => {
    const p = new URLSearchParams({ q: query, mode, limit: "20" })
    if (after) p.set("cursor", after)
    if (filter.tool) p.set("tool", filter.tool)
    if (filter.model) p.set("model", filter.model)
    if (filter.kind) p.set("kind", filter.kind)
    if (filter.errors) p.set("has_error", "true")
    setLoading(true)
    setError("")
    try {
      const r = await read<SearchResult>(`/search?${p}`)
      const next = (r.items ?? []).map((i) => ({
        session: normalizeSession(i),
        match: i.match as Match | undefined,
      }))
      setHits((h) => (after ? [...h, ...next] : next))
      setCursor(r.next_cursor ?? null)
      setNotice(r.notice?.message ?? "")
      setAsked(query)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Search is unavailable.")
    } finally {
      setLoading(false)
    }
  }
  const submit = () => q.trim() && void run(q.trim())

  return (
    <div>
      <form
        className="flex flex-wrap items-center gap-2 px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search what was said"
          aria-label="Search transcript content"
          maxLength={256}
          className="h-8 min-w-0 flex-1 dash-body"
        />
        {modes.length > 1 && (
          <Select value={mode} onValueChange={(v) => v && setMode(String(v))}>
            <SelectTrigger className="h-8 w-36 dash-body" aria-label="Match type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {modes.map((m) => (
                <SelectItem key={m} value={m}>
                  {m[0].toUpperCase() + m.slice(1)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <Button type="submit" size="sm" disabled={!q.trim() || loading}>
          Search
        </Button>
      </form>
      {notice && <p className="px-4 pb-2 dash-control text-fg-muted">{notice}</p>}
      {error && (
        <p role="alert" className="px-4 pb-2 dash-control text-danger">
          {error}
        </p>
      )}
      {asked && !loading && !hits.length && !error && (
        <p className="p-10 text-center dash-body text-fg-faint">No sessions match that search.</p>
      )}
      <ul>
        {hits.map(({ session: s, match }) => (
          <li key={s.id} className="border-b border-border-subtle">
            <button
              type="button"
              data-row={s.id}
              aria-current={s.id === activeId ? "true" : undefined}
              onFocus={() => onActive(s.id)}
              onClick={() => onOpen(s)}
              className={`block w-full px-4 py-2.5 text-left hover:bg-surface-hover focus-visible:bg-surface-hover ${s.id === activeId ? "bg-surface" : ""}`}
            >
              <span className="flex items-baseline justify-between gap-3">
                <span className="truncate dash-body text-fg">{s.title || "Untitled session"}</span>
                <span className="shrink-0 dash-caption text-fg-faint" title={s.started_at ? new Date(s.started_at).toLocaleString() : undefined}>
                  {s.started_at ? ago(s.started_at) : ""}
                </span>
              </span>
              {match?.snippet && (
                <span className="mt-0.5 line-clamp-2 block dash-control text-fg-muted">
                  <Highlighted {...match} />
                </span>
              )}
              <span className="mt-0.5 block truncate dash-caption text-fg-faint">
                {[projectName(s.project), s.tool, s.model].filter(Boolean).join(" · ")}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {cursor && (
        <div className="px-4 py-3">
          <Button variant="outline" size="sm" disabled={loading} onClick={() => void run(asked, cursor)}>
            Load more
          </Button>
        </div>
      )}
    </div>
  )
}
