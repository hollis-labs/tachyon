import { useEffect, useState } from "react"
import { createPluginRegistry } from "@hollis-labs/plugin-registry"
import type { PluginRegistry, PluginRegistrySnapshot } from "@hollis-labs/plugin-registry"

/**
 * PluginLoader fetches the plugin registry from the server and displays
 * the loaded plugins. This is the proof-of-concept demonstrating the
 * plugin-sdk browser integration.
 */
export function PluginLoader() {
  const [snapshot, setSnapshot] = useState<PluginRegistrySnapshot | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    let registry: PluginRegistry | null = null

    async function loadPlugins() {
      try {
        // Create the plugin registry
        registry = createPluginRegistry({
          onDiagnostic: (event) => {
            console.log('[plugin-registry]', event)
          },
        })

        // Fetch the registry response from the server
        const response = await fetch('/api/plugins/registry')
        if (!response.ok) {
          throw new Error(`Failed to fetch registry: ${response.statusText}`)
        }
        const registryResponse = await response.json()

        // Sync the registry with the response
        await registry.sync(registryResponse)

        if (!cancelled) {
          setSnapshot(registry.snapshot())
          setLoading(false)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err))
          setLoading(false)
        }
      }
    }

    loadPlugins()

    return () => {
      cancelled = true
    }
  }, [])

  if (loading) {
    return (
      <div className="rounded-lg border border-border bg-surface p-4">
        <h3 className="text-sm font-medium mb-2">Plugins</h3>
        <p className="text-sm text-subtle">Loading plugins...</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className="rounded-lg border border-border bg-surface p-4">
        <h3 className="text-sm font-medium mb-2">Plugins</h3>
        <p className="text-sm text-red-500">Error: {error}</p>
      </div>
    )
  }

  if (!snapshot) {
    return null
  }

  const pluginCount = snapshot.plugins.length
  const contributionCount = snapshot.contributions.length

  return (
    <div className="rounded-lg border border-border bg-surface p-4">
      <h3 className="text-sm font-medium mb-4">Plugins</h3>
      <div className="space-y-2">
        <p className="text-sm text-subtle">
          Registry Protocol: {snapshot.protocol}
        </p>
        <p className="text-sm text-subtle">
          Plugins Loaded: {pluginCount}
        </p>
        <p className="text-sm text-subtle">
          Contributions: {contributionCount}
        </p>
        {pluginCount > 0 && (
          <div className="mt-4">
            <h4 className="text-xs font-medium mb-2">Registered Plugins</h4>
            <ul className="space-y-1">
              {snapshot.plugins.map((plugin) => (
                <li key={plugin.id} className="text-xs text-subtle">
                  • {plugin.id} {plugin.loaded ? '✓' : '✗'}
                </li>
              ))}
            </ul>
          </div>
        )}
        {snapshot.errors.length > 0 && (
          <div className="mt-4">
            <h4 className="text-xs font-medium mb-2 text-red-500">Errors</h4>
            <ul className="space-y-1">
              {snapshot.errors.map((err) => (
                <li key={err.pluginId} className="text-xs text-red-500">
                  • {err.pluginId}: {err.reason}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  )
}
