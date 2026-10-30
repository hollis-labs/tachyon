// Monochrome proportional meter: passed/pending in neutral steps, failed in danger.
// Domain-free: callers pass counts and labels.
export interface MeterSegment {
  count: number
  tone: 'ok' | 'bad' | 'pending'
  label: string
}

const TONE = { ok: 'bg-fg-faint', bad: 'bg-danger', pending: 'bg-border' } as const

export function SegmentMeter({ segments, label, className = '' }: { segments: readonly MeterSegment[]; label: string; className?: string }) {
  const shown = segments.filter((s) => s.count > 0)
  const alt = shown.map((s) => `${s.count} ${s.label}`).join(', ')
  return (
    <span role="img" aria-label={`${label}: ${alt || 'none'}`} className={`flex h-1 min-w-12 gap-px overflow-hidden bg-surface ${className}`}>
      {shown.map((s) => <span key={s.tone} className={TONE[s.tone]} style={{ flex: s.count }} />)}
    </span>
  )
}
