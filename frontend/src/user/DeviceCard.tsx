import { Link } from "react-router-dom"
import { MODES, type Mode, useMode } from "../mode"
import { Segmented, SetCard, SetRow } from "../settings/parts"

const OPTIONS = MODES.map((m) => ({
  value: m,
  label: { light: "Light", dark: "Dark", system: "System" }[m],
})) as readonly { value: Mode; label: string }[]

/** Same `mode` key as the rail menu and Settings > Appearance. */
export function DeviceCard() {
  const [mode, setMode] = useMode()
  return (
    <SetCard>
      <SetRow title="This device" help="Colour mode applies right away.">
        <Segmented
          name="device-mode"
          label="Colour mode"
          value={mode}
          options={OPTIONS}
          onChange={setMode}
        />
      </SetRow>
      <SetRow sub help="Alerts, quiet hours and defaults live in Settings.">
        <Link to="/settings/appearance" className="text-xs text-fg underline">
          Open Settings
        </Link>
      </SetRow>
    </SetCard>
  )
}
