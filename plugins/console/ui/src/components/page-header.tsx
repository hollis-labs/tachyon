import type { ReactNode } from 'react'

/**
 * Kit PageHeader with a `leading` slot beside the title, vendored ahead of hollis-labs/frontend-support#40
 * (same markup and classes as @hollis-labs/kit-dashboard's PageHeader). Swap back to the kit once it is published.
 */
export function PageHeader({ title, leading, children }: { title: string; leading?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex items-center justify-between border-b border-border bg-bg dash-page-header">
      <div className="flex min-w-0 items-center gap-3">
        <h1 className="dash-page-heading text-text-soft">{title}</h1>
        {leading}
      </div>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  )
}
