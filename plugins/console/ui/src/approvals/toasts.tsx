import { useEffect, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import type { HeldDecision } from "./context"

const TOTAL_MS = 8000

function Toast({ h, onUndo }: { h: HeldDecision; onUndo(id: string): void }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 250)
    return () => clearInterval(t)
  }, [])
  const left = Math.max(0, h.deadline - now)
  return (
    <li className="pointer-events-auto w-72 border border-border bg-surface">
      <div className="flex items-center justify-between gap-3 px-3 py-2">
        <span className="dash-control text-fg">{h.verb} in {Math.ceil(left / 1000)}s</span>
        <Button variant="ghost" size="sm" onClick={() => onUndo(h.itemId)} aria-label={`Undo: ${h.item.request_snapshot.title}`}>Undo</Button>
      </div>
      <div aria-hidden className="h-0.5 bg-border"><div className="h-full bg-fg-muted" style={{ width: `${(left / TOTAL_MS) * 100}%` }} /></div>
    </li>
  )
}

// Bottom-centre toasts for held decisions. Announces the start and "Undone", not every tick.
export function HeldToasts({ held, onUndo, notice }: { held: HeldDecision[]; onUndo(id: string): void; notice: string }) {
  return (
    <>
      <div role="status" aria-live="polite" className="sr-only">{notice}</div>
      {held.length > 0 && (
        <ul aria-label="Pending decisions" className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex flex-col items-center gap-2">
          {held.map((h) => <Toast key={h.itemId} h={h} onUndo={onUndo} />)}
        </ul>
      )}
    </>
  )
}
