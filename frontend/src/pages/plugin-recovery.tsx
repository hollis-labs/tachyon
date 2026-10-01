import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useState } from "react"
import type { ConfigTarget } from "../api/settings"
import { PluginRecovery } from "../components/settings/plugin-recovery"

export function PluginRecoveryPage({
  targets,
  onRefresh,
}: {
  targets: ConfigTarget[]
  onRefresh(): Promise<void>
}) {
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="Plugin recovery" />
      <div className="grid gap-4 p-4">
        <p className="text-sm text-text-soft">
          Host recovery for unloaded plugins. Settings is unavailable; each restart is an explicit
          host action.
        </p>
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        {targets.map((target) => (
          <PluginRecovery
            key={target.id}
            target={target}
            onRefresh={onRefresh}
            onNotice={setNotice}
            onError={setError}
          />
        ))}
      </div>
    </div>
  )
}
