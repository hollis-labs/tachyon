import { Button } from "@hollis-labs/design-components"
import { type DataFreshness, refreshPageData, useDataFreshness } from "@tachyon/host-sdk"
import { useEffect, useState } from "react"
import { useLocation } from "react-router-dom"

const problems: Record<string, string> = {
  configuration: "Configuration error — check the host endpoint and credential settings.",
  authentication: "Access denied — check the credential and its permissions.",
  unreachable: "Cannot reach the remote service.",
  host_unreachable: "Cannot reach Tachyon.",
  timeout: "The remote service timed out.",
  remote: "The remote service returned an error.",
  rate_limited: "The remote service is rate limiting requests.",
  invalid_response: "The remote service returned an invalid response.",
  storage: "Local storage failed — this update may not survive a restart.",
  oversized: "This response exceeds the configured storage limit.",
}

export function freshnessSummary(rows: readonly DataFreshness[], now: number) {
  if (!rows.length) return { label: "Connecting", updatedAt: null, problem: null }
  const failed = rows.find((r) => r.state === "error")
  const stale = rows.find((r) => r.state === "stale")
  const refreshing = rows.some((r) => r.state === "refreshing")
  const imported = rows.filter((r) => r.state === "imported")
  const problem = rows.find((r) => r.problem)?.problem
  const dates = rows.flatMap((r) => (r.updatedAt ? [r.updatedAt] : []))
  // Oldest constituent snapshot: a successful request must not make other
  // data on the page appear more current than it really is.
  const updatedAt = dates.sort()[0] ?? null
  const cached = rows.some(
    (r) =>
      r.state === "cached" ||
      !r.updatedAt ||
      now - Date.parse(r.updatedAt) >= r.freshSeconds * 1000,
  )
  return {
    label: failed
      ? "Connection error"
      : stale
        ? "Stale data"
        : refreshing
          ? "Refreshing"
          : imported.length
            ? "Imported snapshot"
            : cached
              ? "Cached"
              : "Live",
    updatedAt:
      !failed && !stale && !refreshing && imported.length
        ? (imported.flatMap((r) => (r.updatedAt ? [r.updatedAt] : [])).sort()[0] ?? null)
        : updatedAt,
    problem: problem ? (problems[problem] ?? "Data source error.") : null,
  }
}

/** One consistent freshness surface on every routed page. */
export function DataStatus() {
  const { pathname } = useLocation()
  const snapshots = useDataFreshness()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  const scope = pathname.startsWith("/dashboards/transcripts")
    ? "transcripts"
    : pathname.startsWith("/agents")
      ? "activity"
      : pathname.startsWith("/builder")
        ? "catalog"
        : pathname.startsWith("/planner")
          ? "planner"
          : pathname.startsWith("/repos") ||
              pathname.startsWith("/prs") ||
              pathname.startsWith("/pr/")
            ? "forge"
            : "torque"
  const rows = snapshots.filter((r) => r.scope === scope && r.page === pathname)
  const status = freshnessSummary(rows, now)
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-4 py-1.5 dash-support text-muted-foreground">
      <span className={status.problem ? "text-destructive" : "text-foreground"} role="status">
        {status.label === "Live" ? "●" : "○"} {status.label}
      </span>
      <span>
        {status.label === "Imported snapshot" ? "Imported" : "Last updated"}{" "}
        {status.updatedAt ? (
          <time dateTime={status.updatedAt} title={status.updatedAt}>
            {new Date(status.updatedAt).toLocaleString()}
          </time>
        ) : (
          "—"
        )}
      </span>
      {status.problem && <span className="text-destructive">{status.problem}</span>}
      <Button variant="ghost" size="sm" className="ml-auto" onClick={() => void refreshPageData()}>
        Refresh
      </Button>
    </div>
  )
}
