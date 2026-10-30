import { useEffect, useState } from "react"
import type { ApprovalItem } from "./client"

const RISK = {
  low: { text: "Low risk", tone: "border-border text-fg-muted" },
  medium: { text: "Medium risk", tone: "border-border text-fg-muted" },
  high: { text: "High risk", tone: "border-danger bg-danger/10 text-danger" },
} as const

// Risk is always text; danger is the only colour.
export function RiskChip({ risk }: { risk?: ApprovalItem["request_snapshot"]["class"] }) {
  const r = RISK[risk ?? "medium"]
  return <span className={`inline-flex shrink-0 items-center rounded border px-1.5 font-mono dash-caption ${r.tone}`}>{r.text}</span>
}

const URGENT_MS = 5 * 60000

export function ago(at: string, now = Date.now()) {
  const min = Math.max(0, Math.round((now - Date.parse(at)) / 60000))
  if (min < 1) return "just now"
  if (min < 60) return `${min}m ago`
  const h = Math.floor(min / 60)
  return h < 24 ? `${h}h ago` : `${Math.floor(h / 24)}d ago`
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
  const urgent = !!expiresAt && Date.parse(expiresAt) - now < URGENT_MS
  return <span className={`font-mono dash-caption ${urgent ? "text-danger" : "text-fg-faint"}`}>{text}</span>
}
