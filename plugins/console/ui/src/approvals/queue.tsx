import type { KeyboardEvent } from "react"
import { kindLabel, type ApprovalItem } from "./client"
import { Countdown, RiskChip } from "./parts"

const who = (i: ApprovalItem) => i.request_snapshot.source.agent_label ?? i.request_snapshot.source.agent_id

// "Needs a decision" list: a listbox with named options; j/k and arrows move, Enter opens.
export function ApprovalQueue({ items, selectedId, onSelect, empty, history }: {
  items: ApprovalItem[]
  selectedId?: string
  onSelect(id: string): void
  empty: string
  history?: boolean
}) {
  function onKey(e: KeyboardEvent<HTMLUListElement>) {
    const dir = e.key === "ArrowDown" || e.key === "j" ? 1 : e.key === "ArrowUp" || e.key === "k" ? -1 : 0
    if (!dir) return
    e.preventDefault()
    const at = items.findIndex((i) => i.item_id === selectedId)
    const next = items[Math.min(items.length - 1, Math.max(0, at + dir))]
    if (next) onSelect(next.item_id)
  }
  if (!items.length) return <p className="px-4 py-10 text-center dash-body text-fg-faint">{empty}</p>
  return (
    <ul role="listbox" aria-label={history ? "Approval history" : "Needs a decision"} tabIndex={0} onKeyDown={onKey} className="divide-y divide-border/70">
      {items.map((i) => {
        const r = i.request_snapshot
        const selected = i.item_id === selectedId
        return (
          <li
            key={i.item_id}
            role="option"
            aria-selected={selected}
            aria-label={`${r.title}, ${who(i)}, ${r.class ?? "medium"} risk`}
            onClick={() => onSelect(i.item_id)}
            className={`cursor-pointer px-4 py-3 ${selected ? "bg-surface-hover" : "hover:bg-surface-hover/50"}`}
          >
            <div className="flex items-start justify-between gap-2">
              <span className="min-w-0 truncate dash-body text-fg">{r.title}</span>
              {history
                ? <span className="font-mono dash-caption uppercase text-fg-muted">{i.terminal_outcome?.resolution?.response.decision ?? i.state}</span>
                : <RiskChip risk={r.class} />}
            </div>
            <div className="mt-1 flex items-center justify-between gap-2 font-mono dash-caption text-fg-faint">
              <span className="truncate">{kindLabel(r.kind)} · {who(i)}</span>
              {!history && <Countdown expiresAt={r.expires_at} />}
            </div>
          </li>
        )
      })}
    </ul>
  )
}
