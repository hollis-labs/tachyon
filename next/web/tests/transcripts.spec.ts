import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Transcripts behind the real next/ binary's identity and shell, against the transcripts contract stub
// (e2e/transcripts-stub.ts, the old build's fixtures). The transcripts backend is not built yet;
// TACHYON_E2E_TRANSCRIPTS=backend turns the stub off once it is. Transcripts are read-only, so there is no write path.
test.use({ storageState: owner })

const sessions = (page: import("@playwright/test").Page) =>
  page.getByRole("region", { name: "Transcript sessions" })

test("the session list groups transcripts by day", async ({ page }) => {
  await page.goto("/dashboards/transcripts")
  await page.getByRole("tab", { name: "All" }).click()
  await expect(sessions(page).getByRole("button", { name: /^Wire the forge stubs/ })).toBeVisible()
  await expect(
    sessions(page).getByRole("button", { name: /^Fix flaky approvals test/ }),
  ).toBeVisible()
  await expect(sessions(page).getByText("Showing 4 of 4")).toBeVisible()
  await shot(page, "transcripts-list")
})

test("a session opens in the reader with its turns, redacted", async ({ page }) => {
  await page.goto("/dashboards/transcripts")
  await page.getByRole("tab", { name: "All" }).click()
  await sessions(page)
    .getByRole("button", { name: /^Wire the forge stubs/ })
    .click()
  const reader = page.getByLabel("Transcript reader")
  await expect(reader.getByText("Turn 1 text, redacted.").first()).toBeVisible()
  await expect(page).toHaveURL(/[?&]session=/)
  await shot(page, "transcripts-reader")
})

test("a missing scope shows access denied with the backend's message", async ({
  page,
  transcripts,
}) => {
  test.skip(!transcripts.store, "the stub's refusal")
  if (!transcripts.store) return
  transcripts.store.refuse = true
  await page.goto("/dashboards/transcripts")
  await expect(page.getByText("Access denied")).toBeVisible()
  await expect(page.getByText("This needs tachyon.transcripts.read.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect" })).toHaveCount(0)
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the transcript list fits", async ({ page }) => {
    await page.goto("/dashboards/transcripts")
    await page.getByRole("tab", { name: "All" }).click()
    await expect(
      sessions(page).getByRole("button", { name: /^Wire the forge stubs/ }),
    ).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "transcripts-narrow")
  })
})
