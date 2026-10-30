import { MODES, type Mode, useMode } from "../mode"
import { Card } from "./Card"

const LABEL: Record<Mode, string> = { light: "Light", dark: "Dark", system: "System" }
const HINT: Record<Mode, string> = {
  light: "Always light.",
  dark: "Always dark.",
  system: "Follows your device, live.",
}

/** Three preview cards; same `mode` key as the rail menu, applied immediately. */
export function AppearanceSection() {
  const [mode, setMode] = useMode()
  return (
    <section aria-labelledby="settings-appearance-h" className="space-y-4">
      <h2 id="settings-appearance-h" tabIndex={-1} className="sr-only">
        Appearance
      </h2>
      <Card title="Mode" description="Applies immediately on this device.">
        {/* Native radios share a name, so arrow keys move between them (roving focus). */}
        <fieldset aria-label="Colour mode" className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          {MODES.map((m) => (
            <label
              key={m}
              className={`cursor-pointer rounded-md border p-3 transition-colors hover:bg-panel-hover has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring ${
                mode === m ? "border-primary bg-selection" : "border-border"
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
              <span className="mt-2 flex items-center gap-2 text-sm font-medium text-foreground">
                <span
                  aria-hidden="true"
                  className={`flex h-3.5 w-3.5 items-center justify-center rounded-full border ${
                    mode === m ? "border-primary" : "border-border"
                  }`}
                >
                  {mode === m && <span className="h-1.5 w-1.5 rounded-full bg-primary" />}
                </span>
                {LABEL[m]}
              </span>
              <span className="block pl-5 text-xs text-text-subtle">{HINT[m]}</span>
            </label>
          ))}
        </fieldset>
      </Card>
      <Card title="Theme" description="More themes later.">
        <select
          disabled
          aria-label="Theme"
          className="rounded-md border border-border bg-panel px-2 py-1.5 text-sm text-text-subtle"
        >
          <option>Nanite (default)</option>
        </select>
      </Card>
    </section>
  )
}

/** Mini page drawn with the palette's own tokens. data-theme is needed on the same element for the light values to apply. */
function Preview({ mode }: { mode: Mode }) {
  if (mode === "system") {
    return (
      <div
        className="grid grid-cols-2 overflow-hidden rounded-sm border border-border-subtle"
        aria-hidden="true"
      >
        <Half mode="light" />
        <Half mode="dark" />
      </div>
    )
  }
  return (
    <div className="overflow-hidden rounded-sm border border-border-subtle" aria-hidden="true">
      <Half mode={mode} />
    </div>
  )
}

function Half({ mode }: { mode: "light" | "dark" }) {
  return (
    <div data-theme="nanite-default" data-mode={mode} className="space-y-1 bg-bg p-2">
      <div className="h-1.5 w-2/3 rounded-full bg-fg" />
      <div className="h-1.5 w-1/2 rounded-full bg-fg-faint" />
      <div className="h-3 w-1/3 rounded-sm bg-primary" />
    </div>
  )
}
