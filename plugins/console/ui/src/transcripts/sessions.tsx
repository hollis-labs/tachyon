import { useEffect, useRef, useState } from "react"
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  SearchInput,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@hollis-labs/design-components"
import { SlidersHorizontal } from "lucide-react"
import { Segmented } from "../components/segmented"
import {
  compact,
  number,
  projectName,
  type Snapshot,
  type Filters,
  type Session,
} from "./data"

type Facets = Snapshot["facets"]

const ALL = "__all"

export const WINDOWS = [
  { value: "7", label: "7d" },
  { value: "30", label: "30d" },
  { value: "90", label: "90d" },
  { value: "0", label: "All" },
]

const KINDS = [
  { value: "main", label: "Main sessions" },
  { value: "subagent", label: "Subagents" },
]

const dayStart = (d: Date) =>
  new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()

/** "Today", "Yesterday", "Oct 28" (with the year once it is not this one). */
export function dayLabel(iso: string, now = new Date().getTime()) {
  const d = new Date(iso)
  const diff = Math.round((dayStart(new Date(now)) - dayStart(d)) / 86400000)
  if (diff === 0) return "Today"
  if (diff === 1) return "Yesterday"
  const sameYear = d.getFullYear() === new Date(now).getFullYear()
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
  })
}

/** Newest first, grouped by local day; sessions without a date go last. */
export function groupByDay(rows: Session[], now = new Date().getTime()) {
  const at = (s: Session) => (s.started_at ? Date.parse(s.started_at) : -1)
  const groups: { label: string; rows: Session[] }[] = []
  for (const s of [...rows].sort((a, b) => at(b) - at(a))) {
    const label = s.started_at ? dayLabel(s.started_at, now) : "Undated"
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.rows.push(s)
    else groups.push({ label, rows: [s] })
  }
  return groups
}

/** Row stamp: "1h ago" inside two hours, the clock time later today, then the day. */
export function stamp(iso: string, now = new Date().getTime()) {
  const age = now - Date.parse(iso)
  if (age < 7200000) return ago(iso, now)
  return dayLabel(iso, now) === "Today"
    ? new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })
    : ago(iso, now)
}

export function ago(iso: string, now = new Date().getTime()) {
  const s = Math.max(0, Math.floor((now - Date.parse(iso)) / 1000))
  if (s < 60) return "just now"
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return dayLabel(iso, now) === "Yesterday"
    ? "Yesterday"
    : new Date(iso).toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
      })
}

function Pick({
  label,
  value,
  all,
  options,
  onChange,
}: {
  label: string
  value: string
  all: string
  options: { value: string; label: string }[]
  onChange(v: string): void
}) {
  return (
    <Select
      value={value === "" ? ALL : value}
      onValueChange={(v) => onChange(v === null || v === ALL ? "" : String(v))}
    >
      <SelectTrigger className="h-8 w-auto min-w-28 dash-body" aria-label={label}>
        <SelectValue>{(v: unknown) => (v === ALL || v == null ? all : (options.find((o) => o.value === v)?.label ?? String(v)))}</SelectValue>
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ALL}>{all}</SelectItem>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function Controls({
  filter,
  facets,
  set,
}: {
  filter: Filters
  facets?: Facets
  set(patch: Partial<Filters>): void
}) {
  const named = (xs?: string[]) => (xs ?? []).map((x) => ({ value: x, label: x }))
  return (
    <>
      <Pick label="Tool" value={filter.tool} all="All tools" options={named(facets?.tools)} onChange={(tool) => set({ tool })} />
      <Pick label="Model" value={filter.model} all="All models" options={named(facets?.models)} onChange={(model) => set({ model })} />
      <Pick label="Session kind" value={filter.kind} all="All kinds" options={KINDS} onChange={(kind) => set({ kind })} />
      <label className="flex items-center gap-2 dash-control text-fg-muted">
        <Checkbox checked={filter.errors} onCheckedChange={(v) => set({ errors: v === true })} />
        With errors
      </label>
    </>
  )
}

export function WindowPicker({
  filter,
  set,
}: {
  filter: Filters
  set(patch: Partial<Filters>): void
}) {
  return (
    <Segmented
      bare
      label="Time window"
      value={String(filter.days)}
      onChange={(v) => set({ days: Number(v) })}
      items={WINDOWS}
    />
  )
}

export function SessionToolbar({
  filter,
  facets,
  set,
  reset,
  count,
  mode,
  onMode,
  semantic,
}: {
  filter: Filters
  facets?: Facets
  set(patch: Partial<Filters>): void
  reset(): void
  count?: number
  mode: "text" | "semantic"
  onMode(m: "text" | "semantic"): void
  /** The bridge offers semantic matching; without it the segment stays visible but off. */
  semantic: boolean
}) {
  const [sheet, setSheet] = useState(false)
  const active = [filter.tool, filter.model, filter.kind].filter(Boolean).length + (filter.errors ? 1 : 0)
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex min-w-0 basis-96 md:min-w-80 items-center gap-1 max-md:basis-full md:max-w-md md:flex-1">
        <div className="dash-search-adapter min-w-0 flex-1">
          <SearchInput ariaLabel="Search session metadata" placeholder="Search title, project or tag…" value={filter.query} onChange={(query) => set({ query })} debounceMs={0} slashToFocus />
        </div>
        <Segmented bare label="Search mode" value={mode} onChange={onMode} items={[{ value: "text", label: "Text" }, { value: "semantic", label: "Semantic", disabled: !semantic }]} />
      </div>
      <WindowPicker filter={filter} set={set} />
      <div className="hidden flex-wrap items-center gap-2 md:flex">
        <Pick label="Tool" value={filter.tool} all="All tools" options={(facets?.tools ?? []).map((x) => ({ value: x, label: x }))} onChange={(tool) => set({ tool })} />
        <Pick label="Model" value={filter.model} all="All models" options={(facets?.models ?? []).map((x) => ({ value: x, label: x }))} onChange={(model) => set({ model })} />
        <Pick label="Session kind" value={filter.kind} all="All kinds" options={KINDS} onChange={(kind) => set({ kind })} />
        <Button variant={filter.errors ? "secondary" : "outline"} size="sm" className="rounded-full" aria-pressed={filter.errors} onClick={() => set({ errors: !filter.errors })}>With errors</Button>
      </div>
      <Button variant="outline" size="sm" className="md:hidden" onClick={() => setSheet(true)}>
        <SlidersHorizontal className="h-3 w-3" /> Filters{active ? ` (${active})` : ""}
      </Button>
      <span className="ml-auto dash-label text-fg-muted">{count === undefined ? "Searching…" : `${number(count)} sessions`}</span>
      {sheet && (
        <Dialog open onOpenChange={(o) => !o && setSheet(false)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Filters</DialogTitle>
            </DialogHeader>
            <div className="flex flex-col items-start gap-3">
              <Controls filter={filter} facets={facets} set={set} />
            </div>
            <div className="flex justify-between pt-2">
              <Button variant="ghost" size="sm" onClick={reset}>
                Reset
              </Button>
              <Button size="sm" onClick={() => setSheet(false)}>
                Done
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      )}
    </div>
  )
}

const STEP = 20

export function SessionList({
  rows,
  activeId,
  onOpen,
  onActive,
  keys,
}: {
  rows: Session[]
  activeId: string
  onOpen(s: Session): void
  onActive(id: string): void
  /** J/K move through rows (not while typing or reading). */
  keys: boolean
}) {
  const [limit, setLimit] = useState(STEP)
  const box = useRef<HTMLDivElement>(null)
  const shown = rows.slice(0, limit)
  useEffect(() => {
    if (!keys) return
    const onKey = (e: KeyboardEvent) => {
      if ((e.key !== "j" && e.key !== "k") || e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target as HTMLElement
      if (t.closest("input, textarea, select, [contenteditable], [aria-label='Transcript reader']")) return
      const btns = Array.from(box.current?.querySelectorAll<HTMLButtonElement>("[data-row]") ?? [])
      if (!btns.length) return
      const i = btns.findIndex((b) => b.dataset.row === activeId)
      const next = Math.min(btns.length - 1, Math.max(0, i + (e.key === "j" ? 1 : -1)))
      e.preventDefault()
      btns[next].focus()
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [keys, activeId])
  const now = new Date().getTime()
  const more = useRef<HTMLDivElement>(null)
  // Paging follows the scroll: the sentinel under the list asks for the next page.
  useEffect(() => {
    const el = more.current
    if (!el || typeof IntersectionObserver === "undefined") return
    const io = new IntersectionObserver((e) => e[0]?.isIntersecting && setLimit((n) => n + STEP))
    io.observe(el)
    return () => io.disconnect()
  }, [rows.length, limit])
  return (
    <div ref={box}>
      {groupByDay(shown, now).map((g) => (
        <section key={g.label} aria-label={g.label}>
          <h3 className="sticky top-0 z-10 bg-bg px-4 pb-1 pt-3">
            <span className="inline-block rounded-full bg-surface px-2.5 py-0.5 dash-label font-normal normal-case tracking-normal text-fg-secondary">{g.label}</span>
          </h3>
          <ul>
            {g.rows.map((s) => (
              <li key={s.id}>
                <button
                  type="button"
                  data-row={s.id}
                  aria-current={s.id === activeId ? "true" : undefined}
                  onFocus={() => onActive(s.id)}
                  onClick={() => onOpen(s)}
                  className={`flex w-full items-center gap-3 border-l-2 px-4 py-2.5 text-left hover:bg-surface-hover focus-visible:bg-surface-hover ${s.id === activeId ? "border-fg bg-surface" : "border-transparent"}`}
                >
                  <span aria-hidden className="flex size-6 shrink-0 items-center justify-center rounded-full border border-border dash-caption font-medium uppercase text-fg-secondary">{(s.tool || "?")[0]}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate dash-body text-fg" title={s.title}>
                      {s.title || "Untitled session"}
                    </span>
                    <span className="mt-0.5 block truncate dash-support text-fg-muted" title={s.project}>
                      {[projectName(s.project), s.kind === "subagent" ? "subagent" : "main", s.model].filter(Boolean).join(" · ")}
                    </span>
                  </span>
                  <span className="shrink-0 text-right font-mono dash-caption text-fg-muted max-md:hidden">
                    <span className="block" title={s.started_at ? new Date(s.started_at).toLocaleString() : undefined}>
                      {s.started_at ? stamp(s.started_at, now) : "Unknown"}
                    </span>
                    <span className="block">
                      {number(s.turns)} turns{s.tokens ? ` · ${compact(s.tokens)}` : ""}{s.errors ? ` · ${number(s.errors)} err` : ""}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
      <div ref={more} className="px-4 py-3 dash-label text-fg-muted">
        {rows.length ? `Showing ${number(shown.length)} of ${number(rows.length)}${rows.length > shown.length ? " · scroll for more" : ""}` : "0 sessions"}
      </div>
    </div>
  )
}
