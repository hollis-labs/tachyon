import { Checkbox } from "@hollis-labs/design-components"
import type { KeyboardEvent } from "react"
import { Chip } from "../components/chip"
import { kindLabel, type ApprovalItem } from "./client"
import { InitialsAvatar } from "../components/initials-avatar"
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
            <div className="flex items-start gap-3">
              {onCheck && !history && (
                // A 40px target on touch. Only batchable items can be picked; the others keep the space.
                <span
                  onClick={(e) => e.stopPropagation()}
                  className={`-m-2.5 flex shrink-0 items-center justify-center p-2.5 md:-m-1 md:p-1 ${i.batchable ? "" : "invisible"}`}
                >
                  <Checkbox
                    aria-label={`Select ${r.title}`}
                    disabled={!i.batchable}
                    checked={checked?.has(i.item_id) ?? false}
                    onCheckedChange={(v) => onCheck(i.item_id, v)}
                  />
                </span>
              )}
              <InitialsAvatar name={who(i)} className="mt-0.5 size-8" />
              <div className="min-w-0 flex-1">
                <div className={`truncate dash-body text-fg ${selected ? "font-medium" : ""}`}>{r.title}</div>
                <div className="truncate dash-support text-fg-muted">{who(i)} · {kindLabel(r.kind).toLowerCase()}</div>
                <div className="mt-1.5 flex flex-wrap items-center gap-2">
                  {history
                    ? <Chip>{i.terminal_outcome?.resolution?.response.decision ?? i.state}</Chip>
                    : held?.has(i.item_id) ? <Chip>Approving…</Chip> : <RiskChip risk={r.class} />}
                  {!history && (i.deferred_until && Date.parse(i.deferred_until) > Date.now()
                    ? <Chip tone="faint">Snoozed</Chip>
                    : <Countdown expiresAt={r.expires_at} />)}
                </div>
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}
