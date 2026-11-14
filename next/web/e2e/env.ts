import { join } from "node:path"
import { runDir } from "./run-dir"

// Everything the suite needs from the environment. The ports default away from any port a running
// Tachyon uses; the data lives in a fresh run directory (e2e/run-dir.ts).
export const port = process.env.TACHYON_E2E_PORT ?? "28193"
export const backend = `http://127.0.0.1:${port}`
/** A second backend that never gets an owner, for the first-run (no owner) screen. */
export const freshPort = process.env.TACHYON_E2E_FRESH_PORT ?? String(Number(port) + 1)
export const freshBackend = `http://127.0.0.1:${freshPort}`
export const run = runDir()
/** The owner's backend data (HOME, XDG and TACHYON_DATA_DIR live inside). */
export const dir = join(run, "owner")
export const freshDir = join(run, "fresh")
export const authState = join(run, "owner-auth.json")
export const passwordFile = join(dir, "owner-password")
/** Screenshots for the PR, and Playwright's own output. */
export const shots = join(run, "shots")
export const results = join(run, "results")
export const ownerName = "owner"
export const ownerDisplayName = "E2E Owner"
