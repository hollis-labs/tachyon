import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { CAPTION } from '@planner/shared'
import { cn } from '@hollis-labs/design-components'
import type { DrawerMode, DrawerSize } from './model'

const TITLE: Record<DrawerMode, string> = { scratch: 'Scratchpad', terminal: 'Terminal', workspace: 'Workspace' }
const SIZES: { id: DrawerSize; glyph: string; label: string }[] = [
  { id: 'quarter', glyph: '¼', label: 'Quarter height' },
  { id: 'half', glyph: '½', label: 'Half height' },
  { id: 'full', glyph: '⤢', label: 'Full height' },
]
const HEIGHT: Record<DrawerSize, string> = { quarter: 'h-[120px]', half: 'h-[250px]', full: 'h-[max(250px,calc(100dvh-330px))]' }

/**
 * chat.drawer: hidden until opened, no tab menu. A lighter header strip (mode, size, close) over a recessed body,
 * narrower than the composer and tucked under it (the composer overlaps the lower edge), 12px below the stream.
 * At 390 it is a full-width bottom sheet.
 */
export function Drawer({ mode, size, onSize, onClose, children }: { mode: DrawerMode; size: DrawerSize; onSize: (s: DrawerSize) => void; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => { ref.current?.focus() }, [mode])
  return (
    <div className="relative z-0 flex shrink-0 justify-center px-6 max-sm:fixed max-sm:inset-x-0 max-sm:bottom-0 max-sm:z-50 max-sm:px-0">
      <div
        ref={ref}
        tabIndex={-1}
        role="region"
        aria-label={`${TITLE[mode]} drawer`}
        className="-mb-[18px] mt-3 flex w-full max-w-[716px] flex-col overflow-hidden rounded-t-panel border border-b-0 border-surface-hover bg-bg pb-[18px] shadow-[0_-10px_28px_rgba(0,0,0,.45)] focus-visible:outline-none max-sm:mb-0 max-sm:mt-0 max-sm:max-w-none max-sm:pb-0"
      >
        <div className="flex h-[30px] shrink-0 items-center gap-0.5 border-b border-border-subtle bg-surface pl-3 pr-2">
          <span className={CAPTION}>{TITLE[mode]}</span>
          <span className="flex-1" />
          <div role="group" aria-label="Drawer size" className="flex items-center">
            {SIZES.map((s) => (
              <button key={s.id} type="button" aria-pressed={size === s.id} aria-label={s.label} title={s.label} onClick={() => onSize(s.id)} className={cn('grid h-[22px] min-w-[22px] place-items-center rounded-sm px-1 font-mono text-label text-fg-muted hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', size === s.id && 'bg-selection text-fg')}>{s.glyph}</button>
            ))}
          </div>
          <button type="button" aria-label="Close drawer" title="Close (Esc)" onClick={onClose} className="grid size-[22px] place-items-center text-fg-muted hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring"><X aria-hidden className="size-3.5" /></button>
        </div>
        <div className={cn('flex flex-col gap-2 overflow-auto p-3 max-sm:pb-6', HEIGHT[size])}>{children}</div>
      </div>
    </div>
  )
}
