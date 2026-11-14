import { fileURLToPath } from "node:url"
import { defineConfig } from "@playwright/test"
import { backend, dir, freshBackend, freshDir, freshPort, ownerDisplayName, port } from "./e2e/env"

// The real next/ binary (built after `npm run build`, so the UI is embedded) runs on a throwaway
// data directory; the suite drives the embedded UI on the backend's own origin.
// Environment: TACHYON_E2E_BIN (default ../tachyon), TACHYON_E2E_PORT (default 18093),
// TACHYON_E2E_DIR, PLAYWRIGHT_CHANNEL (default the system Chrome; CI sets chromium).
const next = fileURLToPath(new URL("../", import.meta.url))
const bin = process.env.TACHYON_E2E_BIN ?? `${next}tachyon`

/**
 * Wipes `where`, points the data directory into it (TACHYON_DATA_DIR) with an override that only
 * moves the listen address, optionally creates the owner with the CLI (password on stdin, as in
 * production), then runs the server in the foreground.
 */
function server(where: string, listen: string, withOwner: boolean) {
  const env = `HOME='${where}/profile' XDG_DATA_HOME='${where}/profile/data' XDG_CONFIG_HOME='${where}/profile/config' XDG_STATE_HOME='${where}/profile/state' TACHYON_DATA_DIR='${where}/data' SUBSTRATE_CONFIG='${where}/substrate.yaml'`
  const owner = withOwner
    ? ` && head -c 18 /dev/urandom | base64 | tr '/+' '-_' | tr -d '=\\n' > '${where}/owner-password' && env ${env} '${bin}' identity setup --username owner --display-name '${ownerDisplayName}' < '${where}/owner-password'`
    : ""
  return `rm -rf '${where}' && mkdir -p '${where}/data' && printf 'httphost:\\n  addr: "127.0.0.1:${listen}"\\n' > '${where}/substrate.yaml'${owner} && exec env ${env} '${bin}' serve`
}

export default defineConfig({
  testDir: "./tests",
  globalSetup: "./e2e/global-setup.ts",
  outputDir: `${dir}/results`,
  workers: 1,
  fullyParallel: false,
  reporter: [["list"]],
  use: {
    baseURL: backend,
    headless: true,
    channel: process.env.PLAYWRIGHT_CHANNEL ?? "chrome",
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: server(dir, port, true),
      url: `${backend}/healthz`,
      // Never reuse a server: this suite owns the data directory it starts on.
      reuseExistingServer: false,
      timeout: 60_000,
    },
    {
      // A backend with no owner at all: the screen that says how to create one.
      command: server(freshDir, freshPort, false),
      url: `${freshBackend}/healthz`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
})
