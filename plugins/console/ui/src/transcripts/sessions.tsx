import { useEffect, useRef, useState } from "react"
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
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
  { value: "1", label: "24h" },
  { value: "7", label: "7d" },
  { value: "30", label: "30d" },
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
      <SelectTrigger className="h-8 w-44 dash-body" aria-label={label}>
        <SelectValue />
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

export function SessionFilters({
  filter,
  facets,
  set,
  reset,
}: {
  filter: Filters
  facets?: Facets
  set(patch: Partial<Filters>): void
  reset(): void
}) {
  const [sheet, setSheet] = useState(false)
  const active = [filter.tool, filter.model, filter.kind].filter(Boolean).length + (filter.errors ? 1 : 0)
  return (
    <div className="flex flex-wrap items-center gap-2">
      <WindowPicker filter={filter} set={set} />
      <div className="hidden flex-wrap items-center gap-2 sm:flex">
        <Controls filter={filter} facets={facets} set={set} />
        <Button variant="ghost" size="sm" onClick={reset}>
          Reset
        </Button>
      </div>
      <Button variant="outline" size="sm" className="sm:hidden" onClick={() => setSheet(true)}>
        <SlidersHorizontal className="h-3 w-3" /> Filters{active ? ` (${active})` : ""}
      </Button>
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

const STEP = 50

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
  /** J/K move through rows; off while a reader or sheet is open. */
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
      if (t.closest("input, textarea, select, [contenteditable]")) return
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
  return (
    <div ref={box}>
      {groupByDay(shown, now).map((g) => (
        <section key={g.label} aria-label={g.label}>
          <h3 className="sticky top-0 border-b border-border-subtle bg-bg px-4 py-1.5 dash-caption text-fg-faint">
            {g.label}
          </h3>
          <ul>
            {g.rows.map((s) => (
              <li key={s.id} className="border-b border-border-subtle">
                <button
                  type="button"
                  data-row={s.id}
                  aria-current={s.id === activeId ? "true" : undefined}
                  onFocus={() => onActive(s.id)}
                  onClick={() => onOpen(s)}
                  className={`flex w-full items-start gap-3 px-4 py-2.5 text-left hover:bg-surface-hover focus-visible:bg-surface-hover ${s.id === activeId ? "bg-surface" : ""}`}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate dash-body text-fg" title={s.title}>
                      {s.title || "Untitled session"}
                    </span>
                    <span className="mt-0.5 block truncate dash-caption text-fg-faint" title={s.project}>
                      {[projectName(s.project), s.tool, s.model, s.kind === "subagent" ? "subagent" : ""].filter(Boolean).join(" · ")}
                    </span>
                  </span>
                  <span className="shrink-0 text-right dash-caption text-fg-faint">
                    <span className="block" title={s.started_at ? new Date(s.started_at).toLocaleString() : undefined}>
                      {s.started_at ? ago(s.started_at, now) : "Unknown"}
                    </span>
                    <span className="block">
                      {number(s.turns)} turns{s.errors ? ` · ${number(s.errors)} ${s.errors === 1 ? "error" : "errors"}` : ""}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
      <div className="flex items-center justify-between px-4 py-3 dash-control text-fg-faint">
        <span>
          {rows.length ? `Showing ${number(shown.length)} of ${number(rows.length)} · ${compact(rows.reduce((n, s) => n + s.tokens, 0))} tokens` : "0 sessions"}
        </span>
        {rows.length > shown.length && (
          <Button variant="ghost" size="sm" onClick={() => setLimit(limit + STEP)}>
            Show more
          </Button>
        )}
      </div>
    </div>
  )
}
