import { Button } from "@hollis-labs/design-components"
import { useEffect, useRef, useState } from "react"
import { type ConfigTarget, PluginRestartError, restartPlugin } from "../../api/settings"

function retirementTime(value?: string) {
  if (!value) return "Unknown"
  const time = new Date(value)
  return Number.isFinite(time.getTime()) ? time.toLocaleString() : "Unknown"
}

// Shared by normal Settings and the host-only fallback. It never invokes config
// verbs, installs plugins or changes enablement; recovery is one explicit POST.
export function PluginRecovery({
  target,
  onRefresh,
  onBusy,
  onNotice,
  onError,
}: {
  target: ConfigTarget
  onRefresh(): Promise<void> | void
  onBusy?(busy: boolean): void
  onNotice(message: string): void
  onError(message: string): void
}) {
  const [busy, setBusy] = useState(false)
  const flight = useRef(false)
  const active = useRef(true)
  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  async function restart() {
    if (flight.current) return
    flight.current = true
    setBusy(true)
    onBusy?.(true)
    onError("")
    try {
      await restartPlugin(target.id)
      onNotice(`${target.name || target.id} is loaded.`)
      await onRefresh()
    } catch (failure) {
      onError(
        failure instanceof Error
          ? failure.message
          : "Restart outcome unknown. Refresh before retrying.",
      )
      // Busy is explicitly unchanged; preserve the exact retirement snapshot.
      // 404 disappeared and 500 unloaded require a new host snapshot.
      if (failure instanceof PluginRestartError && failure.httpStatus !== 503) await onRefresh()
    } finally {
      flight.current = false
      onBusy?.(false)
      if (active.current) {
        setBusy(false)
        requestAnimationFrame(() => trigger.current?.focus())
      }
    }
  }
  return (
    <section className="min-w-0 space-y-4 rounded-lg border border-border bg-panel-1 p-4">
      <h2 className="font-semibold">{target.name || target.id}</h2>
      <p className="break-all font-mono text-sm">{target.id}</p>
      <p>Unloaded</p>
      <p className="text-sm">Reason: {target.reason || "Unknown"}</p>
      <p className="text-sm">Retired at: {retirementTime(target.retired_at)}</p>
      <p className="text-sm text-text-soft">
        Configuration and plugin operations are unavailable until recovery succeeds.
      </p>
      <Button ref={trigger} variant="outline" disabled={busy} onClick={() => void restart()}>
        {busy ? "Restarting…" : "Restart plugin"}
      </Button>
    </section>
  )
}
