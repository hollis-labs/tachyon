import { Popover, PopoverContent, PopoverTrigger } from "@hollis-labs/design-components"
import { Check, Monitor, Moon, Sun } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { MODES, type Mode, useMode } from "../mode"
import { initials, useProfile } from "./profile"

const ICON = { light: Sun, dark: Moon, system: Monitor } as const
const LABEL: Record<Mode, string> = { light: "Light", dark: "Dark", system: "System" }
const ITEM =
  "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm text-text transition-colors hover:bg-muted"

/** Rail footer avatar: who you are, You, Settings and the colour mode. */
export function AvatarMenu() {
  const profile = useProfile()
  const [mode, setMode] = useMode()
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const go = (to: string) => {
    setOpen(false)
    navigate(to)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        aria-label="Account menu"
        title="Account menu"
        className="flex h-9 w-9 items-center justify-center rounded-full border border-border text-xs font-medium text-foreground transition-colors hover:bg-panel-hover"
      >
        {initials(profile.name)}
      </PopoverTrigger>
      <PopoverContent side="right" align="end" className="w-56 p-1">
        <div role="menu" aria-label="Account">
          <div className="flex items-center gap-2 px-2 py-1.5">
            <span
              aria-hidden="true"
              className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-border text-xs font-medium text-foreground"
            >
              {initials(profile.name)}
            </span>
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-foreground">
                {profile.name || "Operator"}
              </p>
              {profile.email && (
                <p className="truncate text-xs text-text-subtle">{profile.email}</p>
              )}
            </div>
          </div>
          <button type="button" role="menuitem" className={ITEM} onClick={() => go("/user")}>
            You
          </button>
          <button
            type="button"
            role="menuitem"
            className={ITEM}
            onClick={() => go("/settings/appearance")}
          >
            Settings
          </button>
          <fieldset
            aria-label="Colour mode"
            className="m-0 min-w-0 my-1 border-y border-border-subtle py-1"
          >
            {MODES.map((m) => {
              const Icon = ICON[m]
              return (
                <button
                  key={m}
                  type="button"
                  role="menuitemradio"
                  aria-checked={mode === m}
                  className={ITEM}
                  onClick={() => {
                    setMode(m)
                    setOpen(false)
                  }}
                >
                  <Icon className="h-3.5 w-3.5 text-text-subtle" />
                  <span className="flex-1">{LABEL[m]}</span>
                  {mode === m && <Check className="h-3.5 w-3.5" />}
                </button>
              )
            })}
          </fieldset>
          <button
            type="button"
            role="menuitem"
            aria-disabled="true"
            className={`${ITEM} cursor-not-allowed opacity-60`}
          >
            <span className="flex-1">Sign in</span>
            <span className="text-xs text-text-subtle">off</span>
          </button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
