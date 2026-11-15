import { createPluginTorqueClient } from "@tachyon/torque-client"

/**
 * Torque client bound to the console plugin backend: buffered calls go to
 * /api/plugins/console/v1/..., SSE through the host relay
 * (/api/plugins/console/stream/{events,messages,activity}).
 */
export const consoleClient = createPluginTorqueClient("console", ["events", "messages", "activity"])
