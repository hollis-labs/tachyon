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
  title?: string
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
        {title && <p className="font-bold text-fg">{title}</p>}
        {help && <p className="text-xs leading-normal text-fg-muted">{help}</p>}
      </div>
      {children}
    </div>
  )
}

/** Square mono chip: provenance, status and identity labels. */
export function Chip({ children, tone }: { children: ReactNode; tone?: "danger" | "strong" }) {
  const toneClass =
    tone === "danger"
      ? "border-danger text-danger"
      : tone === "strong"
        ? "border-fg text-fg"
        : "border-border text-fg-secondary"
  return (
    <span
      className={`inline-flex h-[22px] items-center gap-1.5 whitespace-nowrap rounded-xs border px-2 font-mono text-caption ${toneClass}`}
    >
      {children}
    </span>
  )
}

/** Light / Dark / System style segmented radios; native radios keep arrow-key roving. */
export function Segmented<T extends string>({
  name,
  label,
  value,
  options,
  onChange,
  stretch,
}: {
  name: string
  label: string
  value: T
  options: readonly { value: T; label: string }[]
  onChange: (v: T) => void
  stretch?: boolean
}) {
  return (
    <fieldset
      aria-label={label}
      className={`m-0 flex min-w-0 gap-0.5 rounded-control border border-border p-0.5 ${stretch ? "w-full" : ""}`}
    >
      {options.map((o) => (
        <label
          key={o.value}
          className={`inline-flex h-[26px] cursor-pointer items-center justify-center rounded-sm px-3 text-label has-[:focus-visible]:outline has-[:focus-visible]:outline-1 has-[:focus-visible]:outline-ring ${
            stretch ? "flex-1" : ""
          } ${value === o.value ? "bg-selection text-fg" : "text-fg-muted"}`}
        >
          <input
            type="radio"
            name={name}
            value={o.value}
            checked={value === o.value}
            onChange={() => onChange(o.value)}
            className="sr-only"
          />
          {o.label}
        </label>
      ))}
    </fieldset>
  )
}
