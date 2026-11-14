import { MODES, type Mode, useMode } from "../mode"

const LABELS: Record<Mode, string> = { light: "Light", dark: "Dark", system: "System" }

/**
 * Settings. Server-backed sections are `settings.region` contributions rendered by the shell from
 * the admin manifest; Appearance is this browser's own choice (colour mode), as in the old app.
 */
export function SettingsPage() {
  const [mode, setMode] = useMode()
  return (
    <div className="page-content stack">
      <section className="panel stack form-width" aria-labelledby="appearance-heading">
        <h2 id="appearance-heading" className="text-title">
          Appearance
        </h2>
        <fieldset className="stack">
          <legend className="text-caption text-fg-muted">Colour mode</legend>
          <div className="flex gap-4">
            {MODES.map((m) => (
              <label key={m} className="flex items-center gap-2">
                <input
                  type="radio"
                  name="mode"
                  value={m}
                  checked={mode === m}
                  onChange={() => setMode(m)}
                />
                {LABELS[m]}
              </label>
            ))}
          </div>
        </fieldset>
      </section>
    </div>
  )
}
