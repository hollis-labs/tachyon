import { Fragment, useEffect, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { stamp } from "./sessions"
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

/**
 * Content search is offered only when the bridge says so; an absent flag or a failed request means no.
 * The status is read once per page load and is never cached across visits.
 */
export function useSearchStatus(): Status | undefined {
  const [status, setStatus] = useState<Status>()
  useEffect(() => {
    let live = true
    read<Status>("/status").then(
      (s) => live && setStatus(s),
      () => live && setStatus({}),
    )
    return () => {
      live = false
    }
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

/** Rows from /search for the toolbar query; the query lives in component state only, never the URL or history. */
export function SearchResults({
  q,
  mode,
  filter,
  activeId,
  onOpen,
  onActive,
  onCount,
  onReset,
}: {
  q: string
  mode: "text" | "semantic"
  filter: Filters
  activeId: string
  onOpen(s: Session): void
  onActive(id: string): void
  onCount(n: number | undefined): void
  onReset(): void
}) {
  const [hits, setHits] = useState<Hit[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [notice, setNotice] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [asked, setAsked] = useState(false)
  const key = JSON.stringify([q, mode, filter.tool, filter.model, filter.kind, filter.errors])

  const run = async (after?: string) => {
    const p = new URLSearchParams({ q, mode, limit: "20" })
    if (after) p.set("cursor", after)
    if (filter.tool) p.set("tool", filter.tool)
    if (filter.model) p.set("model", filter.model)
    if (filter.kind) p.set("kind", filter.kind)
    if (filter.errors) p.set("has_error", "true")
    setLoading(true)
    setError("")
    try {
      const r = await read<SearchResult>(`/search?${p}`, true)
      const next = (r.items ?? []).map((i) => ({ session: normalizeSession(i), match: i.match as Match | undefined }))
      setHits((h) => (after ? [...h, ...next] : next))
      setCursor(r.next_cursor ?? null)
      setNotice(r.notice?.message ?? "")
      setAsked(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Search is unavailable.")
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    const t = setTimeout(() => void run(), 250)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  useEffect(() => {
    onCount(asked && !loading && !error ? hits.length : undefined)
  }, [asked, loading, error, hits.length, onCount])
  useEffect(() => () => onCount(undefined), [onCount])

  const note = [mode === "semantic" ? "Semantic search: best matching session summaries first." : "", notice].filter(Boolean).join(" ")
  return (
    <div>
      {note && <p className="border-b border-border bg-surface/40 px-4 py-2 dash-support text-fg-secondary">{note}</p>}
      {error && (
        <p role="alert" className="flex items-center gap-2 px-4 py-3 dash-control text-danger">
          {error}
          <Button variant="outline" size="sm" onClick={() => void run()}>Retry</Button>
        </p>
      )}
      {loading && <p role="status" className="px-4 py-3 dash-control text-fg-muted">Searching…</p>}
      {asked && !loading && !hits.length && !error && (
        <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
          <p className="dash-body font-medium text-fg">No sessions match these filters</p>
          <p className="dash-support text-fg-muted">Try a wider time window or clear a filter.</p>
          <Button variant="outline" size="sm" onClick={onReset}>Reset filters</Button>
        </div>
      )}
      <ul className={loading ? "opacity-50" : ""} aria-busy={loading}>
        {hits.map(({ session: s, match }) => (
          <li key={s.id}>
            <button
              type="button"
              data-row={s.id}
              aria-current={s.id === activeId ? "true" : undefined}
              onFocus={() => onActive(s.id)}
              onClick={() => onOpen(s)}
              className={`flex w-full items-start gap-3 border-l-2 px-4 py-2.5 text-left hover:bg-surface-hover focus-visible:bg-surface-hover ${s.id === activeId ? "border-fg bg-surface" : "border-transparent"}`}
            >
              <span aria-hidden className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border border-border dash-caption font-medium uppercase text-fg-secondary">{(s.tool || "?")[0]}</span>
              <span className="min-w-0 flex-1">
                <span className="flex items-baseline justify-between gap-3">
                  <span className="truncate dash-body text-fg">{s.title || "Untitled session"}</span>
                  <span className="shrink-0 font-mono dash-caption text-fg-muted" title={s.started_at ? new Date(s.started_at).toLocaleString() : undefined}>
                    {s.started_at ? stamp(s.started_at) : ""}
                  </span>
                </span>
                {match?.snippet && (
                  <span className="mt-0.5 line-clamp-2 block dash-support text-fg-muted">
                    <Highlighted {...match} />
                  </span>
                )}
                <span className="mt-0.5 block truncate dash-support text-fg-muted">
                  {[projectName(s.project), s.kind === "subagent" ? "subagent" : "main", s.model].filter(Boolean).join(" · ")}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      {cursor && (
        <div className="px-4 py-3">
          <Button variant="outline" size="sm" disabled={loading} onClick={() => void run(cursor)}>
            Load more
          </Button>
        </div>
      )}
    </div>
  )
}
