import { seedId } from "../e2e/planner-stub"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// The planner against the contract stub (e2e/planner-stub.ts) behind the real next/ binary's identity and shell.
test.use({ storageState: owner })

const LENSES = [
  { slug: "streams", label: "Streams", db: "workstreams", title: "Substrate migration" },
  { slug: "roadmap", label: "Roadmap", db: "roadmap", title: "Planner on Substrate" },
  { slug: "ideas", label: "Ideas", db: "ideas", title: "Keyboard-first triage" },
  {
    slug: "decisions",
    label: "Decisions",
    db: "decisions",
    title: "Keep react-router as the host router",
  },
  { slug: "risks", label: "Risks", db: "risks", title: "Data loss at cutover" },
  { slug: "inbox", label: "Inbox", db: "inbox", title: "Link from a chat thread" },
] as const

test("landing: / opens the planner's themes with the first theme in focus", async ({ page }) => {
  await page.goto("/")
  await expect(page).toHaveURL(/\/planner\/themes/)
  await expect(page.getByRole("region", { name: "Port the planner" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Planner", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await shot(page, "planner-themes")
})

for (const lens of LENSES) {
  test(`${lens.label}: the lens lists its items and focuses one`, async ({ page }) => {
    await page.goto(`/planner/${lens.slug}`)
    await expect(
      page.getByRole("tab", { name: new RegExp(`^${lens.label} `), selected: true }),
    ).toBeVisible()
    if (lens.slug === "inbox") {
      // The inbox is a triage stack, not a focus pane.
      await expect(
        page
          .getByRole("region", { name: "Inbox triage" })
          .getByRole("heading", { name: lens.title }),
      ).toBeVisible()
    } else {
      await expect(page.getByRole("region", { name: lens.title })).toBeVisible()
      expect(new URL(page.url()).pathname).toMatch(new RegExp(`^/planner/${lens.slug}/`))
    }
  })
}

test("an item opens by deep link, and a reload keeps it", async ({ page }) => {
  const id = seedId("risks", 1)
  await page.goto(`/planner/risks/${id}`)
  await expect(page.getByRole("region", { name: "Data loss at cutover" })).toBeVisible()
  await page.reload()
  await expect(page.getByRole("region", { name: "Data loss at cutover" })).toBeVisible()
  expect(new URL(page.url()).pathname).toBe(`/planner/risks/${id}`)
})

test("a status change sends the item's current revision (main #710)", async ({ page, planner }) => {
  const id = seedId("workstreams", 1)
  await page.goto(`/planner/streams/${id}`)
  const focus = page.getByRole("region", { name: "Substrate migration" })
  await focus.getByRole("button", { name: "active. Move to" }).click()
  await page.getByRole("menuitem", { name: /Paused/ }).click()
  await expect(focus.getByRole("button", { name: "paused. Move to" })).toBeVisible()
  const patch = planner.writes.find((w) => w.method === "PATCH" && w.path === `/workstreams/${id}`)
  expect(patch?.body).toEqual({ patch: { status: "paused" }, expected_revision: 1 })
  expect(planner.store.workstreams[0]).toMatchObject({ status: "paused", rev: 2 })
})

test("create: New makes a theme, which then opens", async ({ page, planner }) => {
  await page.goto("/planner/themes")
  await page.getByRole("button", { name: /^New/ }).click()
  const dialog = page.getByRole("dialog", { name: "New theme" })
  await dialog.getByLabel(/^Title/).fill("Written by the browser suite")
  await dialog.getByRole("button", { name: "Create theme" }).click()
  await expect(page.getByRole("region", { name: "Written by the browser suite" })).toBeVisible()
  const create = planner.writes.find((w) => w.method === "POST" && w.path === "/themes")
  expect(create?.body).toMatchObject({
    id: expect.stringMatching(/^[0-9a-f-]{36}$/),
    fields: { title: "Written by the browser suite" },
  })
})

test("old planner URLs land where they used to", async ({ page }) => {
  for (const [from, to] of [
    ["/planner", /\/planner\/themes/],
    ["/planner/board", /\/planner\/themes/],
    ["/planner/flight", /\/planner\/themes/],
    ["/planner/priorities", /\/planner\/themes/],
    ["/planner/activity", /\/planner\/themes.*facet=need-you/],
    ["/planner/workstreams", /\/planner\/streams/],
    ["/tasks/CW-1", /\/planner\/themes/],
  ] as const) {
    await page.goto(from)
    await expect(page).toHaveURL(to)
  }
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the planner fits, and the bottom tabs keep Planner first", async ({ page }) => {
    await page.goto("/planner/themes")
    await expect(page.getByText("Port the planner").first()).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "planner-narrow")
  })
})
