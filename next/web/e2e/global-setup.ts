import { readFileSync, writeFileSync } from "node:fs"
import { request } from "@playwright/test"
import { authState, backend, ownerName, passwordFile } from "./env"

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
  writeFileSync(passwordFile, password, { mode: 0o600 })
  const response = await api.post("/api/login", { data: { username: ownerName, password } })
  if (!response.ok()) throw new Error(`e2e sign-in failed: ${response.status()}`)
  await api.storageState({ path: authState })
  await api.dispose()
}
