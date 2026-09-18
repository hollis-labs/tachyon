import { EmptyState, SummaryCards } from "@hollis-labs/sysop-ui"
import { useEffect, useState } from "react"
import type { HealthInfo } from "../api/client"
import { useApi } from "../api/context"
import { PluginLoader } from "../plugins/loader"

/**
 * Starter page — polls the same-origin /api/health endpoint and shows the
 * result in the kit's SummaryCards strip. Now includes the plugin loader
 * demonstrating the plugin-sdk integration.
 */
export function DashboardPage() {
  const api = useApi()
  const [health, setHealth] = useState<HealthInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    api
      .getHealth()
      .then((info: HealthInfo) => {
        if (!cancelled) setHealth(info)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [api])

  if (error) {
    return <EmptyState variant="error" title="Could not reach the server" description={error} />
  }

  return (
    <div className="space-y-4 p-4">
      <SummaryCards
        cards={[
          { label: "Server", value: health ? health.status : "…" },
          { label: "UI", value: "ready" },
        ]}
      />
      <PluginLoader />
    </div>
  )
}
