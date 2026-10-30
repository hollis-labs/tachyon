import type { ComponentType, ReactNode } from 'react'
import { ConsolePrefsPanel } from '../prefs/ConsolePrefsPanel'
import { useOptionalAlertsController } from './context'

/** Settings > Alerts. Mounted by the shell's Settings page; edits apply immediately, like the tray. */
export function AlertsSettingsSection({
  card: Card,
}: {
  /** The shell's Settings card, so the section matches its siblings. */
  card?: ComponentType<{ title: string; description?: string; children: ReactNode }>
}) {
  const c = useOptionalAlertsController()
  return (
    <section aria-labelledby="settings-alerts-h" className="max-w-2xl space-y-4">
      <h2 id="settings-alerts-h" tabIndex={-1} className={Card ? 'sr-only' : 'font-sans text-lg font-semibold normal-case tracking-normal text-fg'}>
        Alerts
      </h2>
      {!Card && <p className="-mt-2 dash-control text-fg-muted">Choose what reaches you and how loudly. Changes apply right away on this device.</p>}
      {c ? (
        Card ? (
          <Card title="Alerts" description="What reaches the bell and toasts. Changes apply immediately.">
            <ConsolePrefsPanel prefs={c.prefs} onChange={c.setPrefs} />
          </Card>
        ) : (
          <ConsolePrefsPanel prefs={c.prefs} onChange={c.setPrefs} />
        )
      ) : (
        <p className="text-sm text-text-subtle">Alerts are not enabled.</p>
      )}
    </section>
  )
}
