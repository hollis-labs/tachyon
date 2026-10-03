import { type Envelope, invokeVerb } from "./verbs"

export interface SettingsField {
  key: string
  type: "string" | "boolean" | "number" | "select"
  label: string
  description?: string
  default?: string | boolean | number
  required?: boolean
  // Preserve nonblank path/ID edge spaces; whitespace-only still becomes empty.
  preserve_edge_whitespace?: boolean
  options?: { label: string; value: string }[]
}

export interface ConfigTarget {
  id: string
  name: string
  settings: { fields?: SettingsField[] }
  state?: "unloaded"
  reason?: string
  retired_at?: string
}

export interface TargetConfig {
  target: ConfigTarget
  values: Record<string, string | boolean | number>
  validation: { valid: boolean; errors?: Record<string, string> }
}

export interface ConfigWriteResult {
  plugin: string
  restart_required: boolean
  config: TargetConfig
}

export interface PluginRestartResult {
  id: string
  status: "loaded" | "unloaded" | "unchanged"
  error?: string
}

// Keep all declared outcomes available to forms, including field errors and ask.
export function listConfigTargets(): Promise<Envelope<ConfigTarget[]>> {
  return invokeVerb("config_list")
}

export function getConfig(plugin: string): Promise<Envelope<TargetConfig>> {
  return invokeVerb("config_get", { plugin })
}

export function setConfig(
  plugin: string,
  values: Record<string, string | boolean | number>,
): Promise<Envelope<ConfigWriteResult>> {
  return invokeVerb("config_set", { plugin, values })
}

export function resetConfig(plugin: string): Promise<Envelope<ConfigWriteResult>> {
  return invokeVerb("config_reset", { plugin })
}

export class PluginRestartError extends Error {
  constructor(
    message: string,
    public readonly unloaded: boolean,
    public readonly httpStatus: number,
    public readonly outcome: PluginRestartResult["status"],
  ) {
    super(message)
  }
}

export async function restartPlugin(plugin: string): Promise<PluginRestartResult> {
  const response = await fetch(`/api/plugins/${encodeURIComponent(plugin)}/restart`, {
    method: "POST",
    headers: { Accept: "application/json" },
  })
  const result = (await response.json()) as PluginRestartResult
  if (!response.ok || result.status !== "loaded") {
    throw new PluginRestartError(
      response.status === 503 && result.status === "unchanged"
        ? "Restart is busy; plugin state is unchanged. Try again later."
        : response.status === 404
          ? "Plugin is no longer known to the host. Refresh the plugin list."
          : result.error || `Plugin restart failed (HTTP ${response.status})`,
      response.status !== 404 && result.status === "unloaded",
      response.status,
      result.status,
    )
  }
  return result
}

export function envelopeError<T>(envelope: Envelope<T>): string {
  if (envelope.status === "ask") return envelope.ask.prompt
  if (envelope.status === "error") return envelope.error.message
  return "The request did not complete."
}

export function validationErrors<T>(envelope: Envelope<T>): Record<string, string> {
  if (envelope.status !== "error" || !envelope.error.detail) return {}
  const detail = envelope.error.detail as { errors?: Record<string, unknown> }
  return Object.fromEntries(
    Object.entries(detail.errors ?? {}).filter(
      (entry): entry is [string, string] => typeof entry[1] === "string",
    ),
  )
}

// Host recovery metadata remains available even if config-ops is itself retired.
export async function retiredConfigTargets(): Promise<ConfigTarget[]> {
  const response = await fetch("/api/plugins/registry", { headers: { Accept: "application/json" } })
  if (!response.ok) throw new Error(`Plugin registry unavailable (HTTP ${response.status})`)
  const registry: { retired_plugins?: ConfigTarget[] } = await response.json()
  return registry.retired_plugins ?? []
}
