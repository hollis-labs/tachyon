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
    <li className="pointer-events-auto w-80 max-w-[calc(100vw-2rem)] border border-border bg-bg-elevated shadow-md">
      <div className="flex items-center justify-between gap-3 px-3 py-2">
        <span className="min-w-0">
          <span className="block dash-control text-fg">{h.verb} in {Math.ceil(left / 1000)}s</span>
          <span className="block truncate dash-support text-fg-muted">{h.item.request_snapshot.title}</span>
        </span>
        <Button variant="ghost" size="sm" onClick={() => onUndo(h.itemId)} aria-label={`Undo: ${h.item.request_snapshot.title}`}>Undo</Button>
      </div>
      <div aria-hidden className="h-0.5 bg-border"><div className="h-full bg-fg-muted" style={{ width: `${(left / TOTAL_MS) * 100}%` }} /></div>
    </li>
  )
}

// Bottom-centre toasts for held decisions. Announces the start and "Undone", not every tick.
export function HeldToasts({ held, onUndo, notice }: { held: HeldDecision[]; onUndo(id: string): void; notice: string }) {
  // Sit 12px above the decision bar (its height varies, capped at 40vh); 16px from the edge otherwise.
  const [bottom, setBottom] = useState(16)
  useEffect(() => {
    if (!held.length) return
    const place = () => {
      const bar = document.querySelector('[role="toolbar"][aria-label="Decision"]')
      setBottom(bar ? Math.max(16, window.innerHeight - bar.getBoundingClientRect().top + 12) : 16)
    }
    place()
    const t = setInterval(place, 250)
    window.addEventListener("resize", place)
    return () => {
      clearInterval(t)
      window.removeEventListener("resize", place)
    }
  }, [held.length])
  return (
    <>
      <div role="status" aria-live="polite" className="sr-only">{notice}</div>
      {held.length > 0 && (
        <ul aria-label="Pending decisions" style={{ bottom }} className="pointer-events-none fixed inset-x-0 z-50 flex flex-col items-center gap-2">
          {held.map((h) => <Toast key={h.itemId} h={h} onUndo={onUndo} />)}
        </ul>
      )}
    </>
  )
}
