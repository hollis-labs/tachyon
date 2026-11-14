import { test as base, expect } from "@playwright/test"
import { plannerStub } from "../e2e/planner-stub"

/**
 * Every page gets the planner contract stub (e2e/planner-stub.ts) until B5 serves the planner from the next/
 * binary: the planner is the landing surface, so most journeys pass through it. `planner` exposes the stub's
 * store and the writes it received.
 */
export const test = base.extend<{ planner: Awaited<ReturnType<typeof plannerStub>> }>({
  planner: [
    async ({ page }, use) => {
      await use(await plannerStub(page))
    },
    { auto: true },
  ],
})
export { expect }
