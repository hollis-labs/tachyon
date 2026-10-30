import { cn } from '@hollis-labs/design-components'

export interface Milestone {
  key: string
  label: string
  state: 'reached' | 'next' | 'pending'
  /** Small caption under the label: "done", "next · 4 to go". */
  note: string
}

/** Milestones by order and completion, never by date: a rule across the top, filled up to the last reached one. */
export function MilestoneTrack({ milestones, label }: { milestones: readonly Milestone[]; label: string }) {
  const reached = milestones.filter((m) => m.state === 'reached').length
  const span = Math.max(1, milestones.length - 1)
  return (
    <ol aria-label={label} className="relative flex justify-between px-1.5 max-sm:flex-col max-sm:gap-2.5">
      <span aria-hidden className="absolute inset-x-1.5 top-1.5 border-t border-border max-sm:hidden" />
      <span aria-hidden className="absolute left-1.5 top-1.5 border-t border-fg max-sm:hidden" style={{ width: `calc((100% - 0.75rem) * ${Math.max(0, reached - 1) / span})` }} />
      {milestones.map((m) => (
        <li key={m.key} className="relative flex min-w-28 flex-col items-center gap-1.5 text-center text-xs text-fg-secondary max-sm:min-w-0 max-sm:flex-row max-sm:text-left">
          <i aria-hidden className={cn('z-10 size-3 rounded-full border-2 border-bg outline outline-1', m.state === 'reached' ? 'bg-fg outline-fg' : 'bg-bg outline-fg-muted', m.state === 'pending' && 'outline-dashed')} />
          <span>{m.label}</span>
          <small className="font-mono text-caption text-fg-muted">{m.note}</small>
        </li>
      ))}
    </ol>
  )
}
