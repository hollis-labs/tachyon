import { fileURLToPath } from "node:url"
import { defineConfig } from "@playwright/test"
import {
  backend,
  dir,
  freshBackend,
  freshDir,
  freshPort,
  port,
  results,
  tether,
  tetherPort,
} from "./e2e/env"

// The real next/ binary (built after `npm run build`, so the UI is embedded) runs on a fresh run
// directory (e2e/run-dir.ts); the suite drives the embedded UI on the backend's own origin.
// Environment: TACHYON_E2E_BIN (default ../tachyon), TACHYON_E2E_PORT (default 28193, and the next
// port for the no-owner backend), TACHYON_E2E_DIR (where run directories are made; default the OS
// temp dir), PLAYWRIGHT_CHANNEL (default the system Chrome; CI sets chromium).
const next = fileURLToPath(new URL("../", import.meta.url))
const serve = fileURLToPath(new URL("./e2e/serve.sh", import.meta.url))
const bin = process.env.TACHYON_E2E_BIN ?? `${next}tachyon`
const fake = fileURLToPath(new URL("./e2e/tether-fake.ts", import.meta.url))
/** The chat backend run (TACHYON_E2E_CHAT=backend) points the binary at a local Tether fake, never a real hub. */
const chatBackend = process.env.TACHYON_E2E_CHAT === "backend"

function server(where: string, listen: string, withOwner: boolean, url: string) {
  return {
    command: `sh '${serve}'`,
    env: {
      E2E_BIN: bin,
      E2E_DIR: where,
      E2E_ADDR: `127.0.0.1:${listen}`,
      E2E_OWNER: withOwner ? "1" : "0",
      E2E_TETHER: withOwner && chatBackend ? tether : "",
    },
    url: `${url}/healthz`,
    // Never reuse a server: this suite owns the data directory it starts on, and a port already in
    // use fails the run instead of testing whatever answers there.
    reuseExistingServer: false,
    timeout: 60_000,
  }
}

export default defineConfig({
  testDir: "./tests",
  globalSetup: "./e2e/global-setup.ts",
  outputDir: results,
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
    ...(chatBackend
      ? [
          {
            command: `node '${fake}'`,
            env: { E2E_TETHER_ADDR: `127.0.0.1:${tetherPort}` },
            url: `${tether}/healthz`,
            reuseExistingServer: false,
            timeout: 30_000,
          },
        ]
      : []),
    server(dir, port, true, backend),
    // A backend with no owner at all: the screen that says how to create one.
    server(freshDir, freshPort, false, freshBackend),
  ],
})
