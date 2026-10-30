import { Link } from "react-router-dom"
import { MODES, type Mode, useMode } from "../mode"
import { Card } from "../settings/Card"

const LABEL: Record<Mode, string> = { light: "Light", dark: "Dark", system: "System" }

/** Same `mode` key as the rail menu and Settings > Appearance. */
export function DeviceCard() {
  const [mode, setMode] = useMode()
  return (
    <Card title="This device">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <fieldset
          aria-label="Colour mode"
          className="inline-flex rounded-md border border-border p-0.5"
        >
          {MODES.map((m) => (
            <label
              key={m}
              className={`cursor-pointer rounded-sm px-3 py-1 text-sm has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring ${
                mode === m ? "bg-selection text-foreground" : "text-text-subtle"
              }`}
            >
              <input
                type="radio"
                name="device-mode"
                value={m}
                checked={mode === m}
                onChange={() => setMode(m)}
                className="sr-only"
              />
              {LABEL[m]}
            </label>
          ))}
        </fieldset>
        <Link to="/settings/appearance" className="text-sm text-text-subtle underline">
          Open Settings
        </Link>
      </div>
    </Card>
  )
}
