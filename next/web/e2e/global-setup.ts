import { readFileSync } from "node:fs"
import { request } from "@playwright/test"
import { authState, backend, ownerName, passwordFile } from "./env"
import { BASE, DBS, seed } from "./planner-stub"
import { SESSION_ID } from "./tether-fake"

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
  if (process.env.TACHYON_E2E_CHAT === "backend") await seedChat(api)
  await api.dispose()
}

const CHAT = "/api/plugins/console/v1/chat"
const TURNS = "/api/plugins/console/v1/chat-turns"

/**
 * The chat backend run: the binary syncs the Tether fake's two turns (e2e/tether-fake.ts) into the thread
 * tether:sess-e2e-1. The operator's answer to the first turn goes through the binary's own reply route, and
 * the plan and the shared file are made through its plan and workspace routes, as the stub seeds them.
 */
async function seedChat(api: Awaited<ReturnType<typeof request.newContext>>) {
  const ref = `tether:${SESSION_ID}`
  const thread = `${CHAT}/threads/${encodeURIComponent(ref)}`
  // biome-ignore lint/suspicious/noExplicitAny: seed responses are read for a field or two
  const ok = async (what: string, r: Awaited<ReturnType<typeof api.get>>): Promise<any> => {
    if (!r.ok()) throw new Error(`seeding chat, ${what}: ${r.status()} ${await r.text()}`)
    return r.status() === 204 ? undefined : r.json()
  }
  let inbox: { pending: { item_id: string; kind: string; revision: number }[] } | undefined
  for (let i = 0; i < 30; i++) {
    inbox = await ok(
      "inbox",
      await api.get(`${TURNS}/inbox`, { headers: { "Cache-Control": "no-cache" } }),
    )
    if (inbox && inbox.pending.length >= 2) break
    await new Promise((r) => setTimeout(r, 500))
  }
  const question = inbox?.pending.find((t) => t.kind === "question")
  if (!question)
    throw new Error(`seeding chat: the Tether fake's turns did not sync: ${JSON.stringify(inbox)}`)
  await ok(
    "reply",
    await api.post(`${TURNS}/items/${encodeURIComponent(question.item_id)}/reply`, {
      data: {
        expected_revision: question.revision,
        action: "respond",
        response_text: "How is the port going?",
      },
    }),
  )
  const plan = await ok(
    "plan",
    await api.post(`${thread}/plans`, { data: { title: "Port the chat surface" } }),
  )
  let revision = plan.revision
  for (const title of ["Carry the chat code", "Write the contract stub"]) {
    const step = await ok(
      "step",
      await api.post(`${thread}/plans/${encodeURIComponent(plan.id)}/steps`, {
        data: { title, expected_revision: revision },
      }),
    )
    revision = step.plan_revision
  }
  await ok(
    "workspace",
    await api.post(`${thread}/workspace`, {
      multipart: {
        file: {
          name: "port-notes.md",
          mimeType: "text/markdown",
          buffer: Buffer.from("Carried, not redesigned.\n"),
        },
      },
    }),
  )
}
