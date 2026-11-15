import { createApiContext } from '@hollis-labs/design-app-runtime'
import { pluginApiBase, pluginStreamUrl } from '@tachyon/host-sdk'
import { TorqueApiClient, type TorqueStream } from './api'

/**
 * A Torque client bound to one plugin's routes: buffered calls go through the
 * plugin backend (/api/plugins/{id}/v1/...), SSE through the host relay
 * (/api/plugins/{id}/stream/{name}).
 */
export function createPluginTorqueClient(pluginId: string, streams: TorqueStream[]): TorqueApiClient {
  const urls: Partial<Record<TorqueStream, string>> = {}
  for (const s of streams) urls[s] = pluginStreamUrl(pluginId, s)
  return new TorqueApiClient(`${pluginApiBase(pluginId)}/v1`, urls)
}

/**
 * Shared context. Each plugin wraps its routes (and shell layer) with
 * `<ApiProvider client={its client}>`; the default only serves tests.
 */
export const { ApiProvider, useApi } = createApiContext(new TorqueApiClient('/api/v1'))
