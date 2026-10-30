import { Checkbox } from "@hollis-labs/design-components"
import type { KeyboardEvent } from "react"
import { kindLabel, type ApprovalItem } from "./client"
import { Countdown, RiskChip } from "./parts"

const who = (i: ApprovalItem) => i.request_snapshot.source.agent_label ?? i.request_snapshot.source.agent_id

// "Needs a decision" list: a listbox with named options; j/k and arrows move, Enter opens.
export function ApprovalQueue({ items, selectedId, onSelect, empty, history, held, checked, onCheck }: {
  items: ApprovalItem[]
  selectedId?: string
  onSelect(id: string): void
  empty: string
  history?: boolean
  held?: Set<string>
  // Batch selection: only items the server marks batchable get a checkbox.
  checked?: Set<string>
  onCheck?(id: string, on: boolean): void
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
            className={`group cursor-pointer px-4 py-3 ${held?.has(i.item_id) ? "opacity-50" : ""} ${selected ? "bg-surface/60" : "hover:bg-surface/30"}`}
          >
            <div className="flex items-start gap-2">
              {onCheck && i.batchable && !history && (
                // A 40px target on touch; on desktop the box shows on hover or focus, or once anything is picked.
                <span
                  onClick={(e) => e.stopPropagation()}
                  className={`-m-2.5 flex shrink-0 items-center justify-center p-2.5 md:-m-1 md:p-1 ${checked?.size ? "" : "md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100"}`}
                >
                  <Checkbox
                    aria-label={`Select ${r.title}`}
                    checked={checked?.has(i.item_id) ?? false}
                    onCheckedChange={(v) => onCheck(i.item_id, v)}
                  />
                </span>
              )}
              <span className={`min-w-0 flex-1 truncate dash-body text-fg ${selected ? "font-medium" : ""}`}>{r.title}</span>
              {history
                ? <span className="font-mono dash-caption uppercase text-fg-muted">{i.terminal_outcome?.resolution?.response.decision ?? i.state}</span>
                : held?.has(i.item_id) ? <span className="font-mono dash-caption text-fg-muted">Approving…</span> : <RiskChip risk={r.class} />}
            </div>
            <div className="mt-1 flex items-center justify-between gap-2 font-mono dash-caption text-fg-faint">
              <span className="truncate">{kindLabel(r.kind)} · {who(i)}</span>
              {!history && (i.deferred_until && Date.parse(i.deferred_until) > Date.now()
                ? <span>Snoozed</span>
                : <Countdown expiresAt={r.expires_at} />)}
            </div>
          </li>
        )
      })}
    </ul>
  )
}
