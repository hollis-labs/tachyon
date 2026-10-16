// STUB — owned by the Console overlay porting agent. Replace with the real layer.
import type { ReactNode } from "react"

/** Whole-app layer: console providers, overlays, alert toasts, LinkText slot. */
export function ConsoleLayer({ children }: { children: ReactNode }) {
  return <>{children}</>
}

/** Nav rail footer extras (alerts tray trigger, etc.). */
export function ConsoleNavExtras() {
  return null
}
