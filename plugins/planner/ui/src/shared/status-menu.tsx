import {
  cn,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@hollis-labs/design-components"
import { type KeyboardEvent, type ReactNode, useEffect, useState } from "react"

export interface StatusMenuOption {
  value: string
  label: string
  /** Dot colour classes (a token class), shown before the label. */
  dotClass?: string
  /** Extra key besides the number, e.g. "p". */
  key?: string
}

export interface StatusMenuProps {
  /** The trigger content: usually the status badge for the current value. */
  trigger: ReactNode
  /** Accessible name of the trigger, e.g. "Status: Doing. Change". */
  triggerLabel: string
  /** Heading inside the menu, e.g. "Move to". */
  heading: string
  current: string
  /** Only the valid transitions; the menu never filters them. */
  options: readonly StatusMenuOption[]
  onSelect: (value: string) => void
  disabled?: boolean
  /** When disabled, keep the trigger focusable (aria-disabled) and point it at the element that says why. */
  disabledReasonId?: string
  className?: string
}

/**
 * "Move to..." popover. Items carry a number (1..n) and an optional letter as
 * one-key shortcuts; arrows and Enter work as in any menu, Esc closes and
 * returns focus to the trigger.
 */
export function StatusMenu({
  trigger,
  triggerLabel,
  heading,
  current,
  options,
  onSelect,
  disabled,
  disabledReasonId,
  className,
}: StatusMenuProps) {
  const soft = Boolean(disabled && disabledReasonId)
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (disabled) setOpen(false)
  }, [disabled])
  const choose = (value: string) => {
    setOpen(false)
    if (value !== current) onSelect(value)
  }
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    const k = e.key.toLowerCase()
    const hit = options.find((o, i) => String(i + 1) === k || o.key?.toLowerCase() === k)
    if (hit) {
      e.preventDefault()
      choose(hit.value)
    }
  }
  return (
    <DropdownMenu open={open} onOpenChange={(o) => !soft && setOpen(o)}>
      <DropdownMenuTrigger
        disabled={disabled && !soft}
        aria-disabled={soft || undefined}
        aria-describedby={soft ? disabledReasonId : undefined}
        aria-label={triggerLabel}
        className={cn(
          "inline-flex items-center rounded-md focus-visible:outline-2 disabled:opacity-60 aria-disabled:opacity-60",
          className,
        )}
      >
        {trigger}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" onKeyDown={onKeyDown}>
        <DropdownMenuGroup>
          <DropdownMenuLabel>{heading}</DropdownMenuLabel>
          {options.map((o, i) => (
            <DropdownMenuItem
              key={o.value}
              onClick={() => choose(o.value)}
              aria-current={o.value === current ? "true" : undefined}
            >
              <span aria-hidden className={cn("size-1.5 rounded-full bg-fg-faint", o.dotClass)} />
              <span className="flex-1">{o.label}</span>
              {o.value === current && <span className="dash-caption text-fg-faint">✓</span>}
              <kbd className="font-mono dash-caption text-fg-faint">{o.key ?? i + 1}</kbd>
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
