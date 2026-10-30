// Local segmented control: one line at any width (scrolls sideways when it must),
// sentence-case labels, counts in muted text. Stands in until the kit has one.
export interface SegmentedItem<T extends string> {
  value: T
  label: string
  count?: number
}

export function Segmented<T extends string>({ label, value, onChange, items, className = '' }: {
  label: string
  value: T
  onChange: (v: T) => void
  items: SegmentedItem<T>[]
  className?: string
}) {
  return (
    <div role="tablist" aria-label={label} className={`flex overflow-x-auto border-b border-border px-4 py-2 ${className}`}>
      <div className="inline-flex shrink-0 border border-border">
        {items.map((it, i) => {
          const on = it.value === value
          return (
            <button
              key={it.value}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => onChange(it.value)}
              className={`whitespace-nowrap px-3 py-1 dash-control ${i > 0 ? 'border-l border-border' : ''} ${on ? 'bg-surface font-medium text-fg' : 'text-fg-muted hover:text-fg'}`}
            >
              {it.label}
              {it.count !== undefined && <span className={`ml-1.5 ${on ? 'text-fg-muted' : 'text-fg-faint'}`}>{it.count}</span>}
            </button>
          )
        })}
      </div>
    </div>
  )
}
