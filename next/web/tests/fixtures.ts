import { test as base, expect } from "@playwright/test"
import { BASE, plannerStub, type StubItem } from "../e2e/planner-stub"

/** `TACHYON_E2E_PLANNER=backend` runs against the binary's own planner (B5); otherwise the contract stub answers. */
export const plannerBackend = process.env.TACHYON_E2E_PLANNER === "backend"

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
})
export { expect }
