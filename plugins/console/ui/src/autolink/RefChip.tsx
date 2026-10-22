import { createElement, useEffect, type MouseEvent } from 'react'
import { PreviewCard } from '@base-ui/react/preview-card'
import { buildPreviewCard, type RefIdentity, type RefMeta } from '../refs'
import { useAutolink, useRefState } from './context'
import { RefIcon } from './RefIcon'
import { refHover } from './extensions'
import { PreviewCardView } from './PreviewCardView'

export interface RefChipProps {
  kind: string
  canonical: string
  /** The text as written, passed to the kind's `chipLabel` fallback. Defaults to the canonical id. */
  raw?: string
  meta?: RefMeta
  className?: string
  /** Resolve on mount (default, batched with every other chip on the page) or only on hover/focus. */
  resolve?: 'eager' | 'hover'
  /** Hovercard open delay in ms. Default 350. */
  hoverDelay?: number
  /** Turn the hovercard off (dense tables, where the click target is enough). */
  hovercard?: boolean
}

const CHIP = 'inline-flex max-w-full items-baseline gap-1 border px-1 align-baseline font-mono text-[0.85em] leading-[1.4] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'
const STATE_STYLE: Record<string, string> = {
  found: 'border-border bg-surface text-fg hover:border-fg-faint hover:bg-surface-hover',
  'not-found': 'border-dashed border-border text-fg-faint line-through decoration-fg-faint hover:text-fg-secondary',
  error: 'border-dashed border-warning text-warning hover:bg-surface-hover',
  default: 'border-border bg-surface text-info hover:border-fg-faint hover:bg-surface-hover',
}

/**
 * An inline reference chip: kind icon and short label; a hovercard on hover or
 * keyboard focus (Esc closes it); activation opens the detail drawer, or
 * navigates when the kind has a page and no resolver. Modified clicks (new tab)
 * always fall through to the browser.
 */
export function RefChip({ kind, canonical, raw, meta, className = '', resolve = 'eager', hoverDelay = 350, hovercard = true }: RefChipProps) {
  const { registry, client, activate, hosted } = useAutolink()
  const state = useRefState(kind, canonical)
  const ref: RefIdentity = meta ? { kind, canonical, meta } : { kind, canonical }
  const def = registry.get(kind)
  const card = buildPreviewCard(registry, ref, state)
  const Hover = refHover(kind)
  const label = def?.chipLabel?.(ref, raw ?? canonical) ?? raw ?? canonical

  useEffect(() => {
    if (resolve === 'eager') client.request([{ kind, canonical }])
  }, [client, kind, canonical, resolve])

  const warm = () => {
    if (resolve === 'hover') client.request([{ kind, canonical }])
  }

  const href = card.open.type === 'route' ? card.open.to : card.open.type === 'external' ? card.open.href : undefined
  const interactive = hosted || href !== undefined
  const style = `${CHIP} ${STATE_STYLE[state.status] ?? STATE_STYLE.default} ${className}`
  const onClick = (e: MouseEvent) => {
    const modified = e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0
    if (activate(ref, { modified })) e.preventDefault()
  }

  const common = {
    className: style,
    'data-ref-kind': kind,
    'data-ref-state': state.status,
    onFocus: warm,
    onPointerEnter: warm,
    onClick,
  }
  const body = (
    <>
      <RefIcon token={card.icon} className="h-3 w-3 shrink-0 translate-y-[2px] opacity-70" />
      <span className="min-w-0 truncate">{label}</span>
    </>
  )
  const trigger = href !== undefined
    ? <a {...common} href={href} {...(card.open.type === 'external' ? { target: '_blank', rel: 'noopener noreferrer' } : {})}>{body}</a>
    : interactive
      ? <button type="button" {...common}>{body}</button>
      : <span {...common} tabIndex={0}>{body}</span>

  if (!hovercard) return trigger
  return (
    <PreviewCard.Root>
      <PreviewCard.Trigger delay={hoverDelay} closeDelay={120} render={trigger} />
      <PreviewCard.Portal>
        <PreviewCard.Positioner sideOffset={6} align="start" className="z-[60]">
          <PreviewCard.Popup
            data-testid="ref-hovercard"
            className="w-80 max-w-[calc(100vw-1.5rem)] border border-border bg-popover p-3 text-fg shadow-xl transition-opacity duration-100 data-ending-style:opacity-0 data-starting-style:opacity-0"
          >
            <PreviewCardView card={card} onRetry={() => client.invalidate(kind, canonical)}>
              {Hover && card.phase !== 'not-found' && card.phase !== 'unresolved' ? createElement(Hover, { refId: ref, card }) : undefined}
            </PreviewCardView>
          </PreviewCard.Popup>
        </PreviewCard.Positioner>
      </PreviewCard.Portal>
    </PreviewCard.Root>
  )
}
