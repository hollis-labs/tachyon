import { type Envelope, invokeVerb } from "./verbs"

export interface SettingsField {
  key: string
  type: "string" | "boolean" | "number" | "select"
  label: string
  description?: string
  default?: string | boolean | number
  required?: boolean
  options?: { label: string; value: string }[]
}

export interface ConfigTarget {
  id: string
  name: string
  settings: { fields?: SettingsField[] }
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
  status: "loaded" | "unloaded"
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
      result.error || `Plugin restart failed (HTTP ${response.status})`,
      result.status === "unloaded",
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
