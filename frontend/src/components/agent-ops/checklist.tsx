import { Checkbox, EmptyState } from "@hollis-labs/sysop-ui"
import { Search } from "lucide-react"
import { useMemo, useState } from "react"

export interface ChecklistItem {
  value: string
  label: string
  description?: string
  /** Grouping key for the optional category filter (e.g. a tool's name prefix). */
  category?: string
  /** e.g. a tool not yet synced into the provider's known-tools catalog. */
  disabled?: boolean
  disabledReason?: string
}

interface SearchableChecklistProps {
  items: ChecklistItem[]
  checked: Set<string>
  onToggle: (value: string, next: boolean) => void
  searchPlaceholder?: string
  emptyText?: string
  /** When provided, renders a category filter dropdown above the search box. */
  categoryLabel?: string
  className?: string
}

// A plain, per-row checkbox list: click anywhere on a row to toggle it
// immediately. Deliberately not sysop-ui's TransferList — that component's
// click-to-highlight-then-press-the-arrow-button interaction (built for
// bulk multi-move flows) reads as "nothing happens" for a single-item
// grant/revoke toggle, and doesn't support filtering by an attribute
// beyond the built-in text search.
export function SearchableChecklist({
  items,
  checked,
  onToggle,
  searchPlaceholder = "Search...",
  emptyText = "Nothing here.",
  categoryLabel,
  className,
}: SearchableChecklistProps) {
  const [search, setSearch] = useState("")
  const [category, setCategory] = useState<string>("all")

  const categories = useMemo(() => {
    if (!categoryLabel) return []
    return Array.from(new Set(items.map((i) => i.category).filter((c): c is string => !!c))).sort()
  }, [items, categoryLabel])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return items.filter((item) => {
      if (category !== "all" && item.category !== category) return false
      if (!q) return true
      return (
        item.label.toLowerCase().includes(q) ||
        item.value.toLowerCase().includes(q) ||
        (item.description ?? "").toLowerCase().includes(q)
      )
    })
  }, [items, search, category])

  const checkedInFiltered = filtered.filter((i) => checked.has(i.value)).length

  return (
    <div
      className={`flex min-h-0 flex-col rounded-lg border border-border bg-panel/40 ${className ?? ""}`}
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <label className="flex h-8 min-w-[10rem] flex-1 items-center gap-2 rounded-md border border-border bg-bg px-2 text-text-soft">
          <Search className="h-3.5 w-3.5 shrink-0" />
          <input
            className="min-w-0 flex-1 bg-transparent text-[12px] text-text outline-none placeholder:text-text-subtle"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={searchPlaceholder}
          />
        </label>
        {categoryLabel && categories.length > 0 && (
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            className="h-8 rounded-md border border-border bg-bg px-2 text-[12px] text-text outline-none"
          >
            <option value="all">All {categoryLabel}</option>
            {categories.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        )}
        <span className="font-mono text-[11px] text-text-subtle">
          {checkedInFiltered}/{filtered.length} selected
        </span>
      </div>
      <div className="max-h-72 overflow-y-auto">
        {filtered.length === 0 ? (
          <EmptyState
            variant="no-results"
            title={emptyText}
            description="Try a different search or filter."
          />
        ) : (
          <div className="divide-y divide-border">
            {filtered.map((item) => {
              const isChecked = checked.has(item.value)
              return (
                // The WAI-ARIA custom-checkbox pattern (role="checkbox" +
                // aria-checked + tabIndex on a plain element), not a
                // <button>: the Checkbox below is itself an interactive
                // element, and nesting one interactive element inside
                // another is invalid HTML and risks double-handling the
                // click. The Checkbox is pointer-events-none so every
                // click here — anywhere on the row — is handled exactly
                // once, by this element.
                <div
                  key={item.value}
                  role="checkbox"
                  tabIndex={item.disabled ? -1 : 0}
                  aria-disabled={item.disabled}
                  aria-checked={isChecked}
                  onClick={() => !item.disabled && onToggle(item.value, !isChecked)}
                  onKeyDown={(e) => {
                    if (item.disabled) return
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      onToggle(item.value, !isChecked)
                    }
                  }}
                  className="flex w-full cursor-pointer items-start gap-2.5 px-3 py-2 text-left transition-colors hover:bg-panel/70 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                  title={item.disabled ? item.disabledReason : undefined}
                >
                  <Checkbox
                    checked={isChecked}
                    disabled={item.disabled}
                    tabIndex={-1}
                    className="pointer-events-none mt-0.5 shrink-0"
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-[12px] text-text">
                      {item.label}
                    </span>
                    {item.description && (
                      <span className="block truncate text-[11px] text-text-soft">
                        {item.description}
                      </span>
                    )}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
