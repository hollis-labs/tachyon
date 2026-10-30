import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { CornerDownLeft, Paperclip, PenLine, Plus, SquareTerminal, Inbox } from 'lucide-react'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Chip, Kbd } from '@planner/shared'

export interface ComposerProps {
  value: string
  onChange: (v: string) => void
  onSubmit: (v: string) => void
  agentLabel: string
  busy?: boolean
  disabled?: boolean
  /** Reachability caption shown on the toolbar, e.g. 'Mail only. Reply may take a while.' */
  note?: string | null
  /** Inline error from the last send; the draft is kept. */
  sendError?: string | null
  textareaRef?: React.Ref<HTMLTextAreaElement>
  /** '/' typed into an empty composer, or the Slash command item. */
  onOpenPalette: () => void
  /** Drawer icons and the + menu items that open a drawer mode (the drawer lands in the next slice). */
  onDrawer?: (mode: 'scratch' | 'terminal' | 'workspace') => void
  /** Extra toolbar control after the chips (the reply filter). */
  extra?: React.ReactNode
  /** Attachments shared from the Workspace (strip above the input). */
  strip?: { id: string; label: string }[]
  onRemoveStrip?: (id: string) => void
}

const ICON_BTN = 'inline-grid size-7 place-items-center rounded-control border border-border text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring disabled:opacity-40'

/**
 * chat.composer: a raised input with an attachment strip, a + menu, mode and shell chips, three drawer icons
 * and an enter-glyph Send, plus the footer disclaimer. Enter sends, Shift+Enter is a newline.
 */
export function Composer({ value, onChange, onSubmit, agentLabel, busy, disabled, note, sendError, textareaRef, onOpenPalette, onDrawer, extra, strip = [], onRemoveStrip }: ComposerProps) {
  const [menu, setMenu] = useState(false)
  const own = useRef<HTMLTextAreaElement>(null)
  const area = (textareaRef as React.RefObject<HTMLTextAreaElement>) ?? own
  const wrap = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!menu) return
    const off = (e: MouseEvent) => { if (!wrap.current?.contains(e.target as Node)) setMenu(false) }
    document.addEventListener('mousedown', off)
    return () => document.removeEventListener('mousedown', off)
  }, [menu])
  useEffect(() => {
    const el = area.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [value, area])

  const send = () => { if (value.trim() && !busy && !disabled) onSubmit(value) }
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send() }
    else if (e.key === '/' && value === '' && !e.metaKey && !e.ctrlKey) { e.preventDefault(); onOpenPalette() }
    else if (e.key === 'Escape') setMenu(false)
  }
  const item = 'flex h-7 w-full items-center gap-2 rounded-sm px-2 text-left text-control text-fg-secondary hover:bg-selection hover:text-fg focus-visible:bg-selection focus-visible:outline-none'
  return (
    <div className="shrink-0 px-6 max-sm:px-2">
      <div ref={wrap} className="relative mx-auto w-full max-w-[760px] rounded-panel border border-surface-hover bg-bg-elevated shadow-[0_10px_28px_rgba(0,0,0,.45)]">
        {strip.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-3 pt-2">
            {strip.map((s) => (
              <Chip key={s.id} square>
                {s.label}
                <button type="button" aria-label={`Remove ${s.label}`} onClick={() => onRemoveStrip?.(s.id)} className="ml-1 text-fg-muted hover:text-fg">✕</button>
              </Chip>
            ))}
          </div>
        )}
        <textarea
          ref={area}
          value={value}
          rows={1}
          disabled={disabled}
          aria-label="Message"
          placeholder={disabled ? 'This agent cannot receive messages right now' : `Message ${agentLabel}… (/ commands, @ reference)`}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKeyDown}
          // The kit paints a focused textarea grey (background-color on :focus-visible, unlayered); the golden input stays flat.
          className="block min-h-[54px] w-full resize-none rounded-none border-0 bg-transparent px-3 py-2.5 text-control text-fg placeholder:text-fg-muted focus-visible:bg-transparent! disabled:opacity-60"
        />
        <div className="flex items-center gap-2 border-t border-border-subtle px-2 py-1.5">
          <button type="button" aria-label="Add" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((m) => !m)} className={ICON_BTN}>
            <Plus aria-hidden className="size-3.5" />
          </button>
          <Chip square>NORM</Chip>
          <span className="max-sm:hidden"><Chip square>Shell: ask</Chip></span>
          <span className="max-sm:hidden">{extra}</span>
          {note && <span className="min-w-0 truncate text-label text-fg-muted max-sm:hidden">{note}</span>}
          <span className="flex-1" />
          {/* The drawer opens from these in slice C-C; until then they stay visible and inert. */}
          <button type="button" aria-label="Scratchpad" title="Scratchpad ⌘J" disabled={!onDrawer} onClick={() => onDrawer?.('scratch')} className={ICON_BTN}><PenLine aria-hidden className="size-3.5" /></button>
          <button type="button" aria-label="Terminal" title="Terminal ⌃T" disabled={!onDrawer} onClick={() => onDrawer?.('terminal')} className={ICON_BTN}><SquareTerminal aria-hidden className="size-3.5" /></button>
          <button type="button" aria-label="Workspace" title="Workspace ⌘⇧W" disabled={!onDrawer} onClick={() => onDrawer?.('workspace')} className={ICON_BTN}><Inbox aria-hidden className="size-3.5" /></button>
          <button type="button" onClick={send} disabled={!value.trim() || busy || disabled} className="inline-flex h-7 items-center gap-1.5 rounded-control bg-primary px-3 text-label font-semibold text-primary-fg hover:opacity-90 disabled:opacity-60 focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
            {busy ? 'Sending' : 'Send'} <Kbd className="border-primary-fg/30 text-primary-fg"><CornerDownLeft aria-hidden className="inline size-2.5" /></Kbd>
          </button>
        </div>
        {/* KIT-GAP: a menu popover with a keyboard-hint column; hand-rolled here until the kit has one. */}
        {menu && (
          <div role="menu" aria-label="Add to message" className="absolute bottom-[46px] left-2 z-20 min-w-[220px] rounded-panel border border-surface-hover bg-bg-elevated p-1 shadow-lg">
            <button type="button" role="menuitem" className={cn(item, 'bg-selection text-fg')} onClick={() => setMenu(false)}><Paperclip aria-hidden className="size-3.5" />Attach<Kbd className="ml-auto">⌘U</Kbd></button>
            <button type="button" role="menuitem" className={item} onClick={() => { setMenu(false); onOpenPalette() }}>/ Slash command<Kbd className="ml-auto">/</Kbd></button>
            <button type="button" role="menuitem" className={item} onClick={() => { setMenu(false); onChange(`${value}@`); area.current?.focus() }}>@ Mention<Kbd className="ml-auto">@</Kbd></button>
            <button type="button" role="menuitem" className={item} onClick={() => { setMenu(false); onDrawer?.('workspace') }}><Inbox aria-hidden className="size-3.5" />Add from Workspace<Kbd className="ml-auto">⌘K W</Kbd></button>
            <button type="button" role="menuitem" className={item} onClick={() => { setMenu(false); onDrawer?.('terminal') }}><SquareTerminal aria-hidden className="size-3.5" />Run in Terminal<Kbd className="ml-auto">⌃T</Kbd></button>
          </div>
        )}
      </div>
      {sendError && <p role="alert" className="mx-auto mt-1 max-w-[760px] text-xs text-danger">{sendError} <span className="text-fg-muted">Your draft is kept.</span></p>}
      <p className={cn(CAPTION, 'py-2 text-center text-micro text-fg-muted')}>Agents can be wrong · check anything that matters</p>
    </div>
  )
}
