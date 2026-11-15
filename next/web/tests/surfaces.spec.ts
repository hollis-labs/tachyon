import { expect, test } from "./fixtures"
import { AREAS, owner, shot } from "./helpers"

test.use({ storageState: owner })

// The planner has its own spec (planner.spec.ts); the rest are stubs until their slice ports them.
for (const area of AREAS.filter((a) => a.stub)) {
  test(`${area.label}: reachable from the nav, and its deep link opens it`, async ({ page }) => {
    await page.goto("/planner")
    await page.getByRole("link", { name: area.label, exact: true }).click()
    expect(new URL(page.url()).pathname).toBe(area.route)
    await expect(page.getByRole("heading", { name: area.label, level: 1 })).toBeVisible()
    await expect(page.locator("[data-stub]")).toBeVisible()
    await page.goto(area.deep)
    await expect(page.getByRole("heading", { name: area.label, level: 1 })).toBeVisible()
    expect(new URL(page.url()).pathname).toBe(area.deep)
  })
}

test("a stub surface, for the PR", async ({ page }) => {
  await page.goto("/agents")
  await expect(page.locator("[data-stub]")).toBeVisible()
  await shot(page, "stub-surface")
})

test("old URLs land where they used to", async ({ page }) => {
  // The planner then opens its themes (planner.spec.ts covers its own old slugs).
  for (const [from, to] of [
    ["/", /\/planner\/themes/],
    ["/tasks", /\/planner\/themes/],
    ["/tasks/CW-1", /\/planner\/themes/],
    ["/prs/inbox", /\/repos$/],
  ] as const) {
    await page.goto(from)
    await expect(page).toHaveURL(to)
  }
})

test("settings, account, keys and sessions are in the footer and open", async ({ page }) => {
  await page.goto("/planner")
  await page.getByRole("link", { name: "Settings" }).click()
  await expect(page.getByRole("heading", { name: "Appearance" })).toBeVisible()
  await page.getByRole("link", { name: "Account" }).click()
  await expect(page.getByText("E2E Owner").first()).toBeVisible()
  await page.getByRole("link", { name: "Access keys" }).click()
  await expect(page.getByRole("heading", { name: "Access keys", level: 1 })).toBeVisible()
  await page.getByRole("link", { name: "Active sessions" }).click()
  await expect(page.getByText("This session", { exact: false })).toBeVisible()
})

test("colour mode is the browser's choice and survives a reload", async ({ page }) => {
  await page.goto("/settings")
  await page.getByRole("radio", { name: "Dark" }).check()
  await expect(page.locator("html")).toHaveAttribute("data-mode", "dark")
  await page.reload()
  await expect(page.locator("html")).toHaveAttribute("data-mode", "dark")
  await page.getByRole("radio", { name: "System" }).check()
})

test("an unknown path serves the shell with a 200 and says the page was not found", async ({
  page,
}) => {
  const response = await page.goto("/no-such-page")
  expect(response?.status()).toBe(200)
  await expect(page.getByText("Page not found")).toBeVisible()
  await expect(page.getByRole("link", { name: "Planner", exact: true })).toBeVisible()
})
