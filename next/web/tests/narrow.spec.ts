import { expect, test } from "./fixtures"
import { AREAS, owner, shot } from "./helpers"

// app-shell 0.11.0 at <= 1023px: a bottom tab bar with the first three nav items and More.
test.use({ storageState: owner, viewport: { width: 390, height: 800 } })

test("390px: three tabs and More; More reaches every other area", async ({ page }) => {
  await page.goto("/planner")
  const tabs = page.locator("[data-nav-tab]")
  await expect(page.getByRole("link", { name: "Planner", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "More" })).toBeVisible()
  await shot(page, "narrow")
  for (const area of AREAS.slice(3)) {
    await page.getByRole("button", { name: "More" }).click()
    const sheet = page.getByRole("dialog", { name: "More" })
    await expect(sheet).toBeVisible()
    if (area === AREAS[3]) await shot(page, "narrow-more")
    await sheet.getByRole("link", { name: area.label, exact: true }).click()
    // .first(): a carried page with the kit's PageHeader has a second h1 (substrate-gap CW-20261115-0026).
    await expect(page.getByRole("heading", { name: area.label, level: 1 }).first()).toBeVisible()
    // An area may redirect within itself (Builder opens /builder/agents).
    expect(new URL(page.url()).pathname).toMatch(new RegExp(`^${area.route}(/|$)`))
  }
  expect(await tabs.count()).toBeGreaterThanOrEqual(3)
})

for (const path of ["/planner", "/agents/sess_work", "/settings", "/user", "/identity/keys"]) {
  test(`390px wide: ${path} does not overflow horizontally`, async ({ page }) => {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    const width = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(width).toBeLessThanOrEqual(390)
  })
}
