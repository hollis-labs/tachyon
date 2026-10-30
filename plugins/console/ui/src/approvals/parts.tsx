import { useEffect, useState } from "react"
import type { ApprovalItem } from "./client"

const RISK = {
  low: { text: "Low risk", tone: "border-success/40 text-success" },
  medium: { text: "Medium risk", tone: "border-warning/40 text-warning" },
  high: { text: "High risk", tone: "border-danger/40 text-danger" },
} as const

// Risk is always text; colour only reinforces it.
export function RiskChip({ risk }: { risk?: ApprovalItem["request_snapshot"]["class"] }) {
  const r = RISK[risk ?? "medium"]
  return <span className={`inline-flex shrink-0 items-center rounded border px-1.5 font-mono dash-caption ${r.tone}`}>{r.text}</span>
}

export function timeLeft(expiresAt: string | undefined, now = Date.now()) {
  if (!expiresAt) return undefined
  const ms = Date.parse(expiresAt) - now
  if (Number.isNaN(ms)) return undefined
  if (ms <= 0) return "Expired"
  const min = Math.ceil(ms / 60000)
  if (min < 60) return `${min}m left`
  const h = Math.floor(min / 60)
  return h < 24 ? `${h}h ${min % 60}m left` : `${Math.floor(h / 24)}d left`
}

// Re-renders each 30s; not a live region (the page announces thresholds, not ticks).
export function Countdown({ expiresAt }: { expiresAt?: string }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    if (!expiresAt) return
    const t = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(t)
  }, [expiresAt])
  const text = timeLeft(expiresAt, now)
  if (!text) return null
  return <span className="font-mono dash-caption text-fg-faint">{text}</span>
}
