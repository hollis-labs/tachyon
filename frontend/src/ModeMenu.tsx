import { Popover, PopoverContent, PopoverTrigger } from "@hollis-labs/design-components"
import { Check, Monitor, Moon, Sun } from "lucide-react"
import { useState } from "react"
import { type Mode, useMode } from "./mode"

const OPTIONS: { value: Mode; label: string; icon: typeof Sun }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
]

/** Nav-rail footer quick menu; writes the same `mode` key Settings will. */
export function ModeMenu() {
  const [mode, setMode] = useMode()
  const [open, setOpen] = useState(false)
  const active = OPTIONS.find((o) => o.value === mode) ?? OPTIONS[2]
  const Icon = active.icon

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        aria-label="Colour mode"
        title="Colour mode"
        className="flex h-9 w-9 items-center justify-center rounded-md text-text-subtle transition-colors hover:bg-panel-hover hover:text-foreground"
      >
        <Icon className="h-4 w-4" />
      </PopoverTrigger>
      <PopoverContent side="right" align="end" className="w-44 p-1">
        <div role="menu" aria-label="Colour mode">
          {OPTIONS.map((o) => (
            <button
              key={o.value}
              type="button"
              role="menuitemradio"
              aria-checked={mode === o.value}
              onClick={() => {
                setMode(o.value)
                setOpen(false)
              }}
              className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm text-text transition-colors hover:bg-muted"
            >
              <o.icon className="h-3.5 w-3.5 text-text-subtle" />
              <span className="flex-1">{o.label}</span>
              {mode === o.value && <Check className="h-3.5 w-3.5" />}
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
