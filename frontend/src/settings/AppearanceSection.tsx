import type { ReactNode } from "react"
import { MODES, type Mode, useMode } from "../mode"
import { SetCard, SetRow, SettingsColumn } from "./parts"

const LABEL: Record<Mode, string> = { light: "Light", dark: "Dark", system: "System" }
const NOTE: Record<Mode, string> = {
  light: "Paper and ink",
  dark: "Graphite",
  system: "Follows your OS",
}

/** Three mode cards on the one `mode` key, shared with the rail menu; applied immediately. */
export function AppearanceSection() {
  const [mode, setMode] = useMode()
  return (
    <SettingsColumn
      id="settings-appearance-h"
      title="Appearance"
      intro="Applies right away on this device."
    >
      <SetCard>
        <SetRow
          title="Mode"
          help="System follows your OS and switches at sunrise and sunset settings."
        />
        {/* Native radios share a name, so arrow keys move between them (roving focus). */}
        <fieldset
          aria-label="Colour mode"
          className="grid grid-cols-1 gap-3 px-4 pb-4 pt-1.5 sm:grid-cols-3"
        >
          {MODES.map((m) => (
            <label
              key={m}
              className={`flex cursor-pointer flex-col gap-2 rounded-panel border p-2.5 has-[:focus-visible]:outline has-[:focus-visible]:outline-1 has-[:focus-visible]:outline-offset-1 has-[:focus-visible]:outline-ring ${
                mode === m ? "border-fg bg-selection" : "border-border"
              }`}
            >
              <input
                type="radio"
                name="mode"
                value={m}
                checked={mode === m}
                onChange={() => setMode(m)}
                className="sr-only"
              />
              <Preview mode={m} />
              <span className="flex items-center gap-2 font-bold text-fg">
                <span
                  aria-hidden="true"
                  className={`inline-block size-3 shrink-0 rounded-full border ${
                    mode === m ? "border-fg bg-fg" : "border-border"
                  }`}
                />
                {LABEL[m]}
              </span>
              <span className="text-label text-fg-muted">{NOTE[m]}</span>
            </label>
          ))}
        </fieldset>
      </SetCard>
      <SetCard>
        <SetRow title="Theme" help="One theme for now: Default. More themes can come later.">
          <span
            aria-disabled="true"
            className="inline-flex h-7 items-center rounded-control border border-border bg-elevated px-2.5 text-xs text-fg opacity-50"
          >
            Default
          </span>
        </SetRow>
      </SetCard>
    </SettingsColumn>
  )
}

/** Mini page drawn with the palette's own tokens. data-theme sits on the same element so the light values apply. */
function Preview({ mode }: { mode: Mode }) {
  return (
    <div
      aria-hidden="true"
      className="relative h-[68px] overflow-hidden rounded-control border border-border"
    >
      {mode === "system" ? (
        <>
          <Ground mode="light" className="absolute inset-y-0 left-0 w-1/2" />
          <Ground mode="dark" className="absolute inset-y-0 right-0 w-1/2" />
          <div className="relative flex flex-col gap-[5px] p-2">
            <i className="block h-1.5 w-2/5 rounded-xs bg-fg-secondary" />
            <Bar className="bg-fg-secondary opacity-50" />
            <Bar className="bg-fg-secondary opacity-50" />
          </div>
        </>
      ) : (
        <Ground mode={mode} className="flex size-full flex-col gap-[5px] p-2">
          <i className="block h-1.5 w-2/5 rounded-xs bg-fg" />
          <Bar className="bg-border" />
          <Bar className="bg-border" />
        </Ground>
      )}
    </div>
  )
}

function Bar({ className }: { className: string }) {
  return <b className={`block h-[5px] w-[85%] rounded-xs ${className}`} />
}

function Ground({
  mode,
  className,
  children,
}: {
  mode: "light" | "dark"
  className: string
  children?: ReactNode
}) {
  return (
    <div data-theme="nanite-default" data-mode={mode} className={`bg-bg ${className}`}>
      {children}
    </div>
  )
}
