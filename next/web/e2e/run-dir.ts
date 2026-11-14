import { mkdtempSync, statSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { isAbsolute, join, resolve } from "node:path"

/** The file that marks a directory this suite created; e2e/serve.sh refuses to run without it. */
export const RUN_MARKER = ".tachyon-e2e-run"
const RUN_ENV = "TACHYON_E2E_RUN_DIR"

/**
 * The run directory: created once per run with mkdtemp (under TACHYON_E2E_DIR when set, else the OS
 * temp dir) and marked, then handed to the workers through the environment. Nothing in it existed
 * before this run, so nothing is ever wiped.
 */
export function runDir(): string {
  const existing = process.env[RUN_ENV]
  if (existing) return existing
  const base = process.env.TACHYON_E2E_DIR ?? tmpdir()
  if (!isAbsolute(base) || resolve(base) === "/" || !statSync(base).isDirectory()) {
    throw new Error(
      `TACHYON_E2E_DIR must be an existing absolute directory other than /: "${base}"`,
    )
  }
  const dir = mkdtempSync(join(resolve(base), "tachyon-e2e-"))
  writeFileSync(join(dir, RUN_MARKER), "")
  process.env[RUN_ENV] = dir
  return dir
}
