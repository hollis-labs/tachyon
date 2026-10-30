import { cn } from '@hollis-labs/design-components'

export type SegmentState = 'done' | 'doing' | 'review' | 'blocked' | 'todo'

export interface SegmentedProgressItem {
  state: SegmentState
  count: number
}

export interface SegmentedProgressProps {
  items: readonly SegmentedProgressItem[]
  /** Text equivalent of the whole bar, e.g. "3 of 8 done, 1 blocked". The bar itself is decorative. */
  label: string
  className?: string
}

// Monochrome ramp: done, doing and review are the foreground token at three
// descending opacities, to do is a surface tone, blocked is the only colour.
const SEGMENT_CLASS: Record<SegmentState, string> = {
  done: 'bg-fg',
  doing: 'bg-fg opacity-60',
  review: 'bg-fg opacity-35',
  todo: 'bg-surface-hover',
  blocked: 'bg-danger',
}

const ORDER: readonly SegmentState[] = ['done', 'doing', 'review', 'blocked', 'todo']

/** Counts of items by state as one bar. Segments flex by count; a non-zero segment never vanishes. */
export function SegmentedProgress({ items, label, className }: SegmentedProgressProps) {
  const counts = new Map<SegmentState, number>()
  for (const it of items) counts.set(it.state, (counts.get(it.state) ?? 0) + Math.max(0, it.count))
  const total = [...counts.values()].reduce((a, b) => a + b, 0)
  const done = counts.get('done') ?? 0
  return (
    <div role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} aria-valuetext={label} className={cn('flex h-1.5 w-full gap-px overflow-hidden rounded-full bg-surface-hover', className)}>
      {total > 0 &&
        ORDER.filter((s) => (counts.get(s) ?? 0) > 0).map((s) => (
          <span key={s} aria-hidden className={cn('min-w-1', SEGMENT_CLASS[s])} style={{ flexGrow: counts.get(s), flexBasis: 0 }} />
        ))}
    </div>
  )
}
