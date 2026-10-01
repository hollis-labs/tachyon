import { type ColumnDef, EmptyState } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { DataTable } from "@hollis-labs/kit-dashboard/data"
import { object, runtimeRows, type ServiceStatus, servicesApi } from "../api/services"
import { useVerbs } from "../api/verbs"
import { ReadFeedback, RefreshButton, useServiceRead } from "../components/services/read-state"

export function liveness(value: unknown) {
  return value === true ? "Live" : value === false ? "Registered, not live" : "Unknown"
}
const connectorColumns: ColumnDef<ServiceStatus>[] = [
  {
    key: "id",
    header: "Connector ID",
    cell: (row) => (
      <span className="block truncate" title={row.service_id}>
        {row.service_id}
      </span>
    ),
    className: "min-w-[12rem]",
    sortValue: (row) => row.service_id,
    width: "fill",
  },
  { key: "live", header: "Connector liveness", cell: (row) => liveness(row.live) },
]
const runtimeColumns: ColumnDef<NonNullable<ReturnType<typeof runtimeRows>>[number]>[] = [
  {
    key: "id",
    header: "ID",
    cell: (row) => (
      <span className="block truncate" title={row.id}>
        {row.id}
      </span>
    ),
    className: "min-w-[12rem]",
    sortValue: (row) => row.id,
    width: "fill",
  },
  { key: "status", header: "Status", cell: (row) => row.status },
  { key: "health", header: "Reported health", cell: (row) => row.health },
  { key: "mode", header: "Mode", cell: (row) => row.mode },
]
export function ServiceHealthPage() {
  const verbs = useVerbs()
  const enabled = verbs.has("service_health")
  const state = useServiceRead(enabled, servicesApi.health)
  // Never show a previous healthy/unhealthy result as current during a pending,
  // failed or ask response. The timestamp records the last successful check.
  const data = !state.pending && state.result?.status === "ok" ? state.result.data : null
  const runtime = object(data?.runtime)
  const connectors = Array.isArray(data?.connectors) ? data.connectors : null
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="Service Health">{enabled && <RefreshButton state={state} />}</PageHeader>
      <div className="grid min-w-0 gap-5 p-4">
        <p className="text-sm text-text-muted">
          Connector liveness and managed resource health are separate. These reads are snapshots,
          not a live stream.
        </p>
        {verbs.loading ? (
          <p role="status">Discovering service capabilities…</p>
        ) : !enabled ? (
          <p>Aggregate health is unsupported by this provider.</p>
        ) : (
          <>
            <ReadFeedback state={state} health />
            {!data && <p>Current health: {state.pending ? "Checking" : "Unknown"}</p>}
            {data && (
              <>
                <section aria-labelledby="daemon-heading" className="grid min-w-0 gap-2">
                  <h2 id="daemon-heading" className="font-semibold">
                    Daemon
                  </h2>
                  <p>
                    Daemon running:{" "}
                    {runtime?.daemon_running === true
                      ? "Yes"
                      : runtime?.daemon_running === false
                        ? "No"
                        : "Unknown"}
                  </p>
                  {!runtime && <p>Runtime snapshot unavailable or unrecognized.</p>}
                </section>
                <section aria-labelledby="connectors-heading" className="grid min-w-0 gap-2">
                  <h2 id="connectors-heading" className="font-semibold">
                    Connector liveness
                  </h2>
                  {connectors ? (
                    <DataTable
                      items={connectors}
                      columns={connectorColumns}
                      getRowId={(row) => row.service_id}
                      emptyState={
                        <EmptyState
                          variant="empty"
                          title="No connectors"
                          description="No connector liveness records were reported."
                        />
                      }
                    />
                  ) : (
                    <p>Connector liveness unknown.</p>
                  )}
                </section>
                {(["services", "resources"] as const).map((key) => {
                  const rows = runtimeRows(data.runtime, key)
                  return (
                    <section
                      key={key}
                      aria-labelledby={`${key}-heading`}
                      className="grid min-w-0 gap-2"
                    >
                      <h2 id={`${key}-heading`} className="font-semibold">
                        {key === "services" ? "Runtime service health" : "Resource health"}
                      </h2>
                      {rows ? (
                        <DataTable
                          items={rows}
                          columns={runtimeColumns}
                          getRowId={(row) => row.id}
                          emptyState={
                            <EmptyState
                              variant="empty"
                              title="No reported records"
                              description="The provider returned an empty health list."
                            />
                          }
                        />
                      ) : (
                        <p>Health records unavailable or unrecognized.</p>
                      )}
                    </section>
                  )
                })}
              </>
            )}
          </>
        )}
      </div>
    </div>
  )
}
