// Local segmented control: one line at any width (scrolls sideways when it must),
// sentence-case labels, counts in muted text. Stands in until the kit has one.
export interface SegmentedItem<T extends string> {
  value: T
  label: string
  count?: number
  disabled?: boolean
}

export function Segmented<T extends string>({
  label,
  value,
  onChange,
  items,
  className = '',
  bare = false,
}: {
  label: string
  value: T
  onChange: (v: T) => void
  items: SegmentedItem<T>[]
  className?: string
  /** No bottom rule or outer padding, for use inside a pane's toolbar. */
  bare?: boolean
}) {
  return (
    <div role="tablist" aria-label={label} className={`flex shrink-0 overflow-x-auto ${bare ? '' : 'border-b border-border px-4 py-2'} ${className}`}>
      <div className="inline-flex shrink-0 border border-border">
        {items.map((it, i) => {
          const on = it.value === value
          return (
            <button
              key={it.value}
              type="button"
              role="tab"
              aria-selected={on}
              disabled={it.disabled}
              onClick={() => onChange(it.value)}
              className={`whitespace-nowrap px-2 py-1 sm:px-3 dash-control ${i > 0 ? 'border-l border-border' : ''} ${on ? 'bg-surface font-medium text-fg' : 'text-fg-muted hover:text-fg'} disabled:cursor-not-allowed disabled:opacity-50`}
            >
              {it.label}
              {it.count !== undefined && <span className={`ml-1 sm:ml-1.5 ${on ? 'text-fg-muted' : 'text-fg-faint'}`}>{it.count}</span>}
            </button>
          )
        })}
      </div>
    </div>
  )
}
