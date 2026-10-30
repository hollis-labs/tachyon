import { ConsolePrefsPanel } from '../prefs/ConsolePrefsPanel'
import { useOptionalAlertsController } from './context'

/** Settings > Alerts. Mounted by the shell's Settings page; edits apply immediately, like the tray. */
export function AlertsSettingsSection() {
  const c = useOptionalAlertsController()
  return (
    <section aria-labelledby="settings-alerts-h" className="max-w-xl space-y-4">
      <h2 id="settings-alerts-h" tabIndex={-1} className="sr-only">
        Alerts
      </h2>
      {c ? (
        <div className="rounded-md border border-border bg-panel px-4 py-4">
          <ConsolePrefsPanel prefs={c.prefs} onChange={c.setPrefs} />
        </div>
      ) : (
        <p className="text-sm text-text-subtle">Alerts are not enabled.</p>
      )}
    </section>
  )
}
