import { createApiClient } from "@hollis-labs/design-app-runtime"
import type { HostPluginInfo } from "@tachyon/host-sdk"

// Same-origin: the Go binary serves both this SPA and the API.
const http = createApiClient({ baseUrl: "" })

export interface HealthInfo {
  status: string
  version: string
}

/** Host API client. Plugin data goes through each plugin's own client. */
export const apiClient = {
  getHealth: () => http.get<HealthInfo>("/api/health"),
  getPlugins: () => http.get<{ plugins: HostPluginInfo[] }>("/api/plugins"),
}

export type AppApiClient = typeof apiClient
