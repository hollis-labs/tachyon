/** A small unlabelled line of values over time. The caller supplies the text equivalent elsewhere. */
export function ProgressLine({ values, className }: { values: readonly number[]; className?: string }) {
  if (values.length < 2) return null
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const y = (v: number) => 80 - ((v - lo) / Math.max(1, hi - lo)) * 76
  const pts = values.map((v, i) => `${(i / (values.length - 1)) * 400},${y(v)}`)
  const [lx, ly] = pts[pts.length - 1].split(',')
  return (
    <svg aria-hidden viewBox="0 0 400 84" preserveAspectRatio="none" className={className}>
      <polyline fill="none" stroke="var(--color-fg)" strokeWidth="1.5" vectorEffect="non-scaling-stroke" points={pts.join(' ')} />
      <circle cx={lx} cy={ly} r="3" fill="var(--color-fg)" />
    </svg>
  )
}
