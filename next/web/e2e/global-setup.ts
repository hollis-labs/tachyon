import { readFileSync } from "node:fs"
import { request } from "@playwright/test"
import { authState, backend, ownerName, passwordFile } from "./env"
import { BASE, DBS, seed } from "./planner-stub"

// The owner was created on the host by the server command (see playwright.config.ts). Sign in
// once for the whole run and keep the session cookie in storageState. Writes carry
// X-Requested-With and a same-origin Origin.
export default async function globalSetup() {
  const api = await request.newContext({
    baseURL: backend,
    extraHTTPHeaders: { "X-Requested-With": "tachyon-ui", Origin: backend },
  })
  const state = await (await api.get("/api/setup")).json()
  if (state.setup_required) throw new Error("the owner was not created on the host")
  const password = readFileSync(passwordFile, "utf8").trim()
  const response = await api.post("/api/login", { data: { username: ownerName, password } })
  if (!response.ok()) throw new Error(`e2e sign-in failed: ${response.status()}`)
  await api.storageState({ path: authState })
  if (process.env.TACHYON_E2E_PLANNER === "backend") {
    // The backend run: the binary's own planner gets the stub's seed items, with the same ids.
    const items = seed()
    for (const db of DBS) {
      for (const { id, rev: _rev, created: _c, updated: _u, ...fields } of [
        ...items[db],
      ].reverse()) {
        const r = await api.post(`${BASE}/${db}`, { data: { id, fields } })
        if (!r.ok()) throw new Error(`seeding ${db}/${id}: ${r.status()} ${await r.text()}`)
      }
    }
  }
  await api.dispose()
}
