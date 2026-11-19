import type { ReactNode } from "react"

// Insights panels as in tachyon-transcripts-golden-page@1 (.panel2/.ph/.pb and .brw rows): a bordered panel with a
// sentence-case title and muted meta, and bar rows with the bar between label and count. Kit Panel and BarMeter use
// an uppercase heading with an icon and put the bar under the row.

export function InsightPanel({ title, meta, right, children }: { title: string; meta?: ReactNode; right?: ReactNode; children: ReactNode }) {
  return (
    <section aria-label={title} className="overflow-hidden rounded-panel border border-border">
      <header className="flex items-center gap-2.5 border-b border-border-subtle px-3.5 py-2.5 text-xs">
        <h3 className="font-sans text-xs font-semibold normal-case tracking-normal text-fg">{title}</h3>
        {meta && <span className="text-label text-fg-muted">{meta}</span>}
        <span className="flex-1" />
        {right && <span className="text-label text-fg-muted">{right}</span>}
      </header>
      {children}
    </section>
  )
}

export interface BarRow { key: string; label: string; value: number }

export function BarRows({ rows, format, onSelect, empty }: { rows: BarRow[]; format(n: number): string; onSelect(row: BarRow): void; empty: string }) {
  const max = Math.max(1, ...rows.map((r) => r.value))
  if (!rows.length) return <p className="px-3.5 py-8 text-center text-xs text-fg-faint">{empty}</p>
  return (
    <ul className="flex flex-col gap-2.5 px-3.5 py-3">
      {rows.map((r) => (
        <li key={r.key}>
          <button
            type="button"
            onClick={() => onSelect(r)}
            title={`${r.label}: ${format(r.value)} sessions`}
            className="group grid w-full grid-cols-[130px_1fr_52px] items-center gap-2.5 rounded-control text-left text-xs text-fg-secondary hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring"
          >
            <span className="truncate">{r.label}</span>
            <span aria-hidden className="h-1.5 overflow-hidden rounded-full bg-border-subtle">
              <span className="block h-full rounded-full bg-fg-muted group-hover:bg-fg-secondary" style={{ width: `${Math.round((r.value / max) * 100)}%` }} />
            </span>
            <span className="text-right font-mono text-fg-muted">{format(r.value)}</span>
          </button>
        </li>
      ))}
    </ul>
  )
}
