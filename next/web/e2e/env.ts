import { tmpdir } from "node:os"
import { join } from "node:path"

// Everything the suite needs from the environment, with defaults for a local run.
export const port = process.env.TACHYON_E2E_PORT ?? "18093"
export const backend = `http://127.0.0.1:${port}`
/** A second backend that never gets an owner, for the first-run (no owner) screen. */
export const freshPort = process.env.TACHYON_E2E_FRESH_PORT ?? String(Number(port) + 1)
export const freshBackend = `http://127.0.0.1:${freshPort}`
/** A throwaway data directory; wiped when the backend starts. HOME and XDG live inside it too. */
export const dir = process.env.TACHYON_E2E_DIR ?? join(tmpdir(), `tachyon-e2e-${port}`)
export const freshDir = `${dir}-fresh`
export const authState = join(dir, "owner-auth.json")
export const passwordFile = join(dir, "owner-password")
/** Screenshots for the PR body land here (inside the throwaway directory unless overridden). */
export const shots = process.env.TACHYON_E2E_SHOTS ?? join(dir, "shots")
export const ownerName = "owner"
export const ownerDisplayName = "E2E Owner"
