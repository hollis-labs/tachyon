import type { ReactNode } from "react"

/** Page column shared by every Settings section: heading, one-line intro, then cards. */
export function SettingsColumn({
  id,
  title,
  intro,
  children,
}: {
  id: string
  title: string
  intro: string
  children: ReactNode
}) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3.5">
      <h2
        id={id}
        tabIndex={-1}
        className="font-sans text-lg font-semibold normal-case leading-normal tracking-normal text-fg outline-none"
      >
        {title}
      </h2>
      <p className="text-fg-muted">{intro}</p>
      {children}
    </section>
  )
}

/** Flat bordered group of rows. */
export function SetCard({ children }: { children: ReactNode }) {
  return <div className="flex flex-col rounded-panel border border-border">{children}</div>
}

/** Title + helper on the left, control on the right. `sub` adds the divider above. */
export function SetRow({
  title,
  help,
  sub,
  children,
}: {
  title: string
  help?: string
  sub?: boolean
  children?: ReactNode
}) {
  return (
    <div
      className={`flex flex-wrap items-center justify-between gap-4 px-4 py-3.5 ${
        sub ? "border-t border-border-subtle" : ""
      }`}
    >
      <div>
        <p className="font-bold text-fg">{title}</p>
        {help && <p className="text-xs leading-normal text-fg-muted">{help}</p>}
      </div>
      {children}
    </div>
  )
}
