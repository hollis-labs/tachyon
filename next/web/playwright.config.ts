import { randomUUID } from "node:crypto"
import { fileURLToPath } from "node:url"
import { defineConfig } from "@playwright/test"
import {
  backend,
  dir,
  fragments,
  fragmentsPort,
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
/** The transcripts backend run (TACHYON_E2E_TRANSCRIPTS=backend) reads the local Fragments Engine fake. */
const transcriptsBackend = process.env.TACHYON_E2E_TRANSCRIPTS === "backend"
const fragmentsFake = fileURLToPath(new URL("./e2e/fragments-fake.ts", import.meta.url))
/** A throwaway bearer for this run only, shared by the fake and the binary's token file. */
const fragmentsToken = randomUUID()
/** The approvals backend run (TACHYON_E2E_APPROVALS=backend) applies e2e/approvals-seed.sql before serving. */
const approvalsBackend = process.env.TACHYON_E2E_APPROVALS === "backend"
const approvalsSeed = fileURLToPath(new URL("./e2e/approvals-seed.sql", import.meta.url))
/** The Agent Ops backend run (TACHYON_E2E_AGENTS=backend) applies e2e/agents-seed.sql before serving. */
const agentsBackend = process.env.TACHYON_E2E_AGENTS === "backend"
const agentsSeed = fileURLToPath(new URL("./e2e/agents-seed.sql", import.meta.url))

function server(where: string, listen: string, withOwner: boolean, url: string) {
  return {
    command: `sh '${serve}'`,
    env: {
      E2E_BIN: bin,
      E2E_DIR: where,
      E2E_ADDR: `127.0.0.1:${listen}`,
      E2E_OWNER: withOwner ? "1" : "0",
      E2E_TETHER: withOwner && (chatBackend || agentsBackend) ? tether : "",
      E2E_AGENTS_CONTROLS: withOwner && agentsBackend ? "1" : "",
      E2E_APPROVALS_SEED: withOwner && approvalsBackend ? approvalsSeed : "",
      E2E_FRAGMENTS: withOwner && transcriptsBackend ? fragments : "",
      E2E_FRAGMENTS_TOKEN: withOwner && transcriptsBackend ? fragmentsToken : "",
      E2E_AGENTS_SEED: withOwner && agentsBackend ? agentsSeed : "",
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
    ...(chatBackend || agentsBackend
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
    ...(transcriptsBackend
      ? [
          {
            command: `node '${fragmentsFake}'`,
            env: {
              E2E_FRAGMENTS_ADDR: `127.0.0.1:${fragmentsPort}`,
              E2E_FRAGMENTS_TOKEN: fragmentsToken,
            },
            port: Number(fragmentsPort),
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
