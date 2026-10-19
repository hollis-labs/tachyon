import { useLayoutEffect, useMemo, useRef } from 'react'
import { robotSprite, type AgentState } from '@console/fleet/lib/agent-activity'

/** Reorder motion: rows glide from their old slot to the new one. */
export function useFlip(order: string) {
  const ref = useRef<HTMLDivElement>(null)
  const prev = useRef(new Map<string, number>())
  useLayoutEffect(() => {
    const root = ref.current
    if (!root) return
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    const next = new Map<string, number>()
    root.querySelectorAll<HTMLElement>('[data-flip]').forEach((el) => {
      const top = el.getBoundingClientRect().top
      next.set(el.dataset.flip!, top)
      const was = prev.current.get(el.dataset.flip!)
      if (reduce || was === undefined || Math.abs(was - top) < 1) return
      el.style.transition = 'none'
      el.style.transform = `translateY(${was - top}px)`
      requestAnimationFrame(() => {
        el.style.transition = 'transform 500ms cubic-bezier(0.2, 0.8, 0.2, 1)'
        el.style.transform = ''
      })
    })
    prev.current = next
  }, [order])
  return ref
}

export function PixelRobot({ id, size = 48, state = 'idle' }: { id: string; size?: number; state?: AgentState }) {
  const sprite = useMemo(() => robotSprite(id), [id])
  const dim = state === 'offline'
  const body = `hsl(${sprite.hue} ${dim ? 10 : 65}% ${dim ? 38 : 58}%)`
  const shade = `hsl(${sprite.hue} ${dim ? 10 : 65}% ${dim ? 26 : 38}%)`
  const eye = state === 'blocked' ? '#fbbf24' : state === 'offline' ? '#52525b' : '#a7f3d0'
  const isEye = (r: number, c: number) => sprite.eyes.some(([er, ec]) => er === r && ec === c)
  return (
    <svg viewBox="0 0 8 8" width={size} height={size} shapeRendering="crispEdges" role="img" aria-label="Robot avatar" className={state === 'working' ? 'agent-bob' : ''}>
      {sprite.pixels.map((row, r) =>
        row.map((on, c) =>
          on && !isEye(r, c) ? <rect key={`${r}-${c}`} x={c} y={r} width={1} height={1} fill={r >= 5 ? shade : body} /> : null,
        ),
      )}
      {sprite.eyes.map(([r, c]) => (
        <rect key={`e${c}`} x={c} y={r} width={1} height={1} fill={eye} className={state === 'offline' ? '' : 'agent-blink'} />
      ))}
    </svg>
  )
}

/** A stepped, 8-bit style bar graph of recent activity; the newest bar pulses while working. */
export function ActivityBars({ series, state, width = 120, height = 32, fluid = false, scaleMax }: { series: number[]; state: AgentState; width?: number; height?: number; fluid?: boolean; scaleMax?: number }) {
  const max = scaleMax ?? Math.max(1, ...series)
  const gap = 1
  const bw = (width - gap * (series.length - 1)) / series.length
  const color = state === 'blocked' ? '#fbbf24' : state === 'offline' ? '#52525b' : '#34d399'
  return (
    <svg
      width={fluid ? '100%' : width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio={fluid ? 'none' : undefined}
      shapeRendering="crispEdges"
      role="img"
      aria-label="Activity over the last 24 hours"
      className="shrink-0"
    >
      {series.map((v, i) => {
        const h = v === 0 ? 1 : Math.max(2, Math.round((v / max) * height))
        const last = i === series.length - 1
        return (
          <rect
            key={i}
            x={i * (bw + gap)}
            y={height - h}
            width={bw}
            height={h}
            fill={color}
            opacity={v === 0 ? 0.25 : 0.45 + 0.55 * (i / series.length)}
            className={last && state === 'working' ? 'agent-pulse-bar' : ''}
          />
        )
      })}
    </svg>
  )
}
