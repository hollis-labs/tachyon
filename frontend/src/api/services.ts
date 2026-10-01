import { invokeVerb } from "./verbs"

// These definitions describe connectors; liveness does not imply resource health.
export interface Service {
  id: string
  version: string
  resource_types: string[] | null
  capabilities: unknown
  config: unknown
  operations: unknown
}
export interface ServiceStatus {
  service_id: string
  live: boolean
}
export interface ServiceHealth {
  runtime: unknown
  connectors: ServiceStatus[] | null
}
export const servicesApi = {
  list: () => invokeVerb<Service[] | null>("service_list"),
  read: (id: string) => invokeVerb<Service>("service_read", { service_id: id }),
  status: (id: string) => invokeVerb<ServiceStatus>("service_status", { service_id: id }),
  health: () => invokeVerb<ServiceHealth>("service_health"),
}

export function object(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}
export function text(value: unknown): string {
  return typeof value === "string" && value.trim() ? value : "Unknown"
}
export function reportedHealth(value: unknown): string {
  return value === true ? "Healthy" : value === false ? "Unhealthy" : "Unknown"
}

// Runtime is opaque at the adapter boundary. Validate each field before display;
// do not infer relationships between connector, legacy-service and resource IDs.
export function runtimeRows(value: unknown, key: "services" | "resources") {
  const rows = object(value)?.[key]
  if (!Array.isArray(rows)) return null
  return rows.flatMap((entry) => {
    const row = object(entry)
    const id = row?.[key === "services" ? "service_id" : "resource_id"]
    if (!row || typeof id !== "string" || !id.trim()) return []
    return [
      {
        id,
        status:
          key === "services"
            ? row.health_configured === false
              ? "Not configured"
              : row.health_configured === true
                ? "Configured"
                : "Unknown"
            : text(row.status),
        health:
          key === "services" && row.health_configured !== true
            ? "Unknown"
            : reportedHealth(row.healthy),
        mode: text(row.mode),
      },
    ]
  })
}
