import { test as base, expect } from "@playwright/test"
import { approvalsStub } from "../e2e/approvals-stub"
import { chatStub } from "../e2e/chat-stub"
import { BASE, plannerStub, type StubItem } from "../e2e/planner-stub"

/** `TACHYON_E2E_PLANNER=backend` runs against the binary's own planner (B5); otherwise the contract stub answers. */
export const plannerBackend = process.env.TACHYON_E2E_PLANNER === "backend"
/** `TACHYON_E2E_CHAT=backend` runs against the binary's own chat (B6); otherwise the chat contract stub answers. */
export const chatBackend = process.env.TACHYON_E2E_CHAT === "backend"
/** `TACHYON_E2E_APPROVALS=backend` runs against the binary's own approvals (B7); otherwise the stub answers. */
export const approvalsBackend = process.env.TACHYON_E2E_APPROVALS === "backend"

export interface PlannerWrite {
  method: string
  path: string
  body: unknown
}

/**
 * Every page records the planner writes it sends (`planner.writes`, paths relative to the planner API). With the
 * stub (e2e/planner-stub.ts) the planner API is answered from a seeded in-memory store (`planner.store`); in the
 * backend run the binary answers, seeded with the same items by e2e/global-setup.ts.
 */
export const test = base.extend<{
  planner: { writes: PlannerWrite[]; store: Record<string, StubItem[]> | null }
  chat: { store: ReturnType<typeof import("../e2e/chat-stub").chatSeed> | null }
  approvals: { store: ReturnType<typeof import("../e2e/approvals-stub").approvalsSeed> | null }
}>({
  planner: [
    async ({ page }, use) => {
      const writes: PlannerWrite[] = []
      page.on("request", (r) => {
        const { pathname } = new URL(r.url())
        if (r.method() === "GET" || !pathname.startsWith(`${BASE}/`)) return
        const data = r.postData()
        writes.push({
          method: r.method(),
          path: pathname.slice(BASE.length),
          body: data ? JSON.parse(data) : undefined,
        })
      })
      const store = plannerBackend ? null : (await plannerStub(page)).store
      await use({ writes, store })
    },
    { auto: true },
  ],
  chat: [
    async ({ page }, use) => {
      await use({ store: chatBackend ? null : (await chatStub(page)).store })
    },
    { auto: true },
  ],
  approvals: [
    async ({ page }, use) => {
      await use({ store: approvalsBackend ? null : (await approvalsStub(page)).store })
    },
    { auto: true },
  ],
})
export { expect }
