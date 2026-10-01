import { Button, type ColumnDef, EmptyState, SearchInput } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { DataTable } from "@hollis-labs/kit-dashboard/data"
import { useCallback, useRef, useState } from "react"
import { object, type Service, servicesApi, text } from "../api/services"
import { useVerbs } from "../api/verbs"
import { LargeDialog } from "../components/agent-ops/large-dialog"
import { ReadFeedback, RefreshButton, useServiceRead } from "../components/services/read-state"
import { liveness } from "./service-health"

export function ServicesPage() {
  const verbs = useVerbs()
  const catalog = useServiceRead(verbs.has("service_list"), servicesApi.list)
  const health = useServiceRead(
    verbs.has("service_list") && verbs.has("service_health"),
    servicesApi.health,
  )
  const [search, setSearch] = useState("")
  const [type, setType] = useState("")
  const [live, setLive] = useState("")
  const [selected, setSelected] = useState<Service | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const statuses =
    !health.pending && health.result?.status === "ok" ? (health.result.data.connectors ?? []) : []
  const statusFor = (id: string) => liveness(statuses.find((row) => row.service_id === id)?.live)
  const items = catalog.result?.status === "ok" ? (catalog.result.data ?? []) : []
  const filtered = items.filter((row) => {
    const types = row.resource_types ?? []
    return (
      (!search || `${row.id} ${types.join(" ")}`.toLowerCase().includes(search.toLowerCase())) &&
      (!type || types.includes(type)) &&
      (!live || statusFor(row.id) === live)
    )
  })
  function open(row: Service) {
    trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setSelected(row)
  }
  const columns: ColumnDef<Service>[] = [
    {
      key: "id",
      header: "Connector ID",
      cell: (row) => (
        <span className="block truncate" title={row.id}>
          {row.id}
        </span>
      ),
      className: "min-w-[12rem]",
      sortValue: (row) => row.id,
      width: "fill",
    },
    {
      key: "version",
      header: "Version",
      cell: (row) => row.version,
      sortValue: (row) => row.version,
    },
    {
      key: "types",
      header: "Resource types",
      cell: (row) => row.resource_types?.join(", ") || "None declared",
    },
    {
      key: "live",
      header: "Connector liveness",
      cell: (row) => (health.pending ? "Checking" : statusFor(row.id)),
    },
    {
      key: "detail",
      header: "Details",
      cell: (row) => (
        <Button variant="outline" aria-label={`Details for ${row.id}`} onClick={() => open(row)}>
          Details
        </Button>
      ),
    },
  ]
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="All Services">
        <Button
          variant="outline"
          disabled={catalog.pending || health.pending}
          onClick={() => {
            void catalog.refresh()
            void health.refresh()
          }}
        >
          Refresh
        </Button>
      </PageHeader>
      <div className="grid min-w-0 gap-4 p-4">
        <p className="text-sm text-text-muted">
          Registered connector definitions. A live connector does not imply its managed resources
          are healthy.
        </p>
        {verbs.loading ? (
          <p role="status">Discovering service capabilities…</p>
        ) : !verbs.has("service_list") ? (
          <p>Connector listing is unsupported by this provider.</p>
        ) : (
          <>
            <p className="text-sm font-medium">Connector definitions</p>
            <ReadFeedback state={catalog} />
            {items.length > 0 && (catalog.pending || catalog.result?.status !== "ok") && (
              <p className="text-sm">Previous connector definitions shown; refresh incomplete.</p>
            )}
            {verbs.has("service_health") ? (
              <>
                <p className="text-sm font-medium">Connector liveness</p>
                <ReadFeedback state={health} health />
              </>
            ) : (
              <p>Aggregate liveness unavailable. Individual status may be available in Details.</p>
            )}
            <div className="flex flex-wrap gap-3">
              <SearchInput
                value={search}
                onChange={setSearch}
                placeholder="Search connector ID or resource type"
                ariaLabel="Search connectors"
              />
              <label className="grid gap-1 text-sm">
                Resource type
                <select
                  className="rounded border border-border-strong bg-surface p-2"
                  aria-label="Resource type"
                  value={type}
                  onChange={(event) => setType(event.target.value)}
                >
                  <option value="">All types</option>
                  {Array.from(new Set(items.flatMap((row) => row.resource_types ?? [])))
                    .sort()
                    .map((entry) => (
                      <option key={entry}>{entry}</option>
                    ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Connector liveness
                <select
                  className="rounded border border-border-strong bg-surface p-2"
                  aria-label="Connector liveness"
                  value={live}
                  onChange={(event) => setLive(event.target.value)}
                >
                  <option value="">All states</option>
                  {["Live", "Registered, not live", "Unknown"].map((entry) => (
                    <option key={entry}>{entry}</option>
                  ))}
                </select>
              </label>
            </div>
            <DataTable
              items={filtered}
              columns={columns}
              getRowId={(row) => row.id}
              emptyState={
                <EmptyState
                  variant={items.length ? "no-results" : "empty"}
                  title={items.length ? "No matching connectors" : "No connector definitions"}
                  description={
                    catalog.pending
                      ? "Waiting for connector definitions."
                      : "Adjust filters or refresh the provider."
                  }
                />
              }
            />
          </>
        )}
      </div>
      {selected && (
        <ServiceDetail
          key={selected.id}
          service={selected}
          onClose={() => {
            setSelected(null)
            trigger.current?.focus()
          }}
        />
      )}
    </div>
  )
}
function ServiceDetail({ service, onClose }: { service: Service; onClose: () => void }) {
  const verbs = useVerbs()
  const read = useCallback(() => servicesApi.read(service.id), [service.id])
  const status = useCallback(() => servicesApi.status(service.id), [service.id])
  const definition = useServiceRead(verbs.has("service_read"), read)
  const current = useServiceRead(verbs.has("service_status"), status)
  const row = definition.data ?? service
  const schema = object(row.config)
  const fields = Array.isArray(schema?.fields)
    ? Array.from(
        new Map(
          schema.fields.flatMap((entry) => {
            const field = object(entry)
            return typeof field?.name === "string" ? [[field.name, field] as const] : []
          }),
        ).values(),
      )
    : []
  const capabilities = Object.keys(object(row.capabilities) ?? {})
  const operations = Array.isArray(row.operations)
    ? row.operations.flatMap((entry) => {
        const name = object(entry)?.name
        return typeof name === "string" ? [name] : []
      })
    : Object.keys(object(row.operations) ?? {})
  const live =
    !current.pending && current.result?.status === "ok"
      ? liveness(current.result.data.live)
      : current.pending
        ? "Checking"
        : "Unknown"
  return (
    <LargeDialog
      open
      onClose={onClose}
      title={`Connector: ${service.id}`}
      description="Definition metadata only. Configuration is a schema, not stored credentials."
      footer={
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div className="grid gap-4">
        {verbs.has("service_read") ? (
          <>
            <RefreshButton state={definition} />
            <ReadFeedback state={definition} />
          </>
        ) : (
          <p>Detail read unsupported; catalog metadata shown.</p>
        )}
        <dl className="grid grid-cols-[auto_1fr] gap-2 text-sm">
          <dt>ID</dt>
          <dd>{row.id}</dd>
          <dt>Version</dt>
          <dd>{row.version}</dd>
          <dt>Resource types</dt>
          <dd>{row.resource_types?.join(", ") || "None declared"}</dd>
          <dt>Connector liveness</dt>
          <dd>{live}</dd>
        </dl>
        {verbs.has("service_status") ? (
          <>
            <RefreshButton state={current} />
            <ReadFeedback state={current} />
          </>
        ) : (
          <p>Individual connector status unsupported.</p>
        )}
        <section className="grid min-w-0 gap-2">
          <h2 className="font-semibold">Declared capabilities</h2>
          <p>{capabilities.join(", ") || "No recognized capability metadata"}</p>
        </section>
        <section className="grid min-w-0 gap-2">
          <h2 className="font-semibold">Declared operations</h2>
          <p>{operations.join(", ") || "No recognized operation metadata"}</p>
          <p className="text-sm text-text-muted">Operations cannot be invoked here.</p>
        </section>
        <section className="grid min-w-0 gap-2">
          <h2 className="font-semibold">Configuration schema</h2>
          {fields.length ? (
            <ul className="grid gap-2 text-sm">
              {fields.map((entry) => {
                const field = object(entry)
                return (
                  <li key={text(field?.name)}>
                    {text(field?.name)} · {text(field?.type)} ·{" "}
                    {field?.required === true
                      ? "Required"
                      : field?.required === false
                        ? "Optional"
                        : "Requirement unknown"}
                  </li>
                )
              })}
            </ul>
          ) : (
            <p>No recognized schema fields.</p>
          )}
        </section>
      </div>
    </LargeDialog>
  )
}
