import { MODES, type Mode, useMode } from "../mode"

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
      <h2
        id="settings-appearance-h"
        tabIndex={-1}
        className="text-lg font-semibold text-foreground"
      >
        Appearance
      </h2>
      <fieldset aria-label="Colour mode" className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        {MODES.map((m) => (
          <label
            key={m}
            className={`cursor-pointer rounded-md border p-3 transition-colors hover:bg-panel-hover has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring ${
              mode === m ? "border-primary" : "border-border"
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
            <span className="mt-2 block text-sm font-medium text-foreground">{LABEL[m]}</span>
            <span className="block text-xs text-text-subtle">{HINT[m]}</span>
          </label>
        ))}
      </fieldset>
      <label className="block max-w-xs space-y-1">
        <span className="text-xs text-text-subtle">Theme</span>
        <select
          disabled
          aria-label="Theme"
          className="w-full rounded-md border border-border bg-panel px-2 py-1.5 text-sm text-text-subtle"
        >
          <option>Nanite (default)</option>
        </select>
        <span className="block text-xs text-text-subtle">More themes later.</span>
      </label>
    </section>
  )
}

/** Mini page drawn with the mode's own tokens via a scoped data-mode. */
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
    <div data-mode={mode} className="space-y-1 bg-bg p-2">
      <div className="h-1.5 w-2/3 rounded-full bg-fg" />
      <div className="h-1.5 w-1/2 rounded-full bg-fg-faint" />
      <div className="h-3 w-1/3 rounded-sm bg-primary" />
    </div>
  )
}
