import type { ReactNode } from "react"

/** Flat bordered group used across Settings sections. */
export function Card({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <div className="rounded-md border border-border bg-panel">
      <div className="border-b border-border-subtle px-4 py-3">
        <h3 className="text-sm font-medium text-foreground">{title}</h3>
        {description && <p className="text-xs text-text-subtle">{description}</p>}
      </div>
      <div className="px-4 py-3">{children}</div>
    </div>
  )
}
