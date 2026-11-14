import type { Page } from "@playwright/test"
import { BASE, seedId } from "../e2e/planner-stub"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// The planner behind the real next/ binary's identity and shell: against the contract stub (e2e/planner-stub.ts),
// or with TACHYON_E2E_PLANNER=backend against the binary's own planner, seeded with the same items.
test.use({ storageState: owner })

/** Reads an item the way the page does (same origin, same session; the stub answers it when it is installed). */
const readItem = (page: Page, path: string) =>
  page.evaluate(
    (url) => fetch(url, { headers: { "X-Requested-With": "tachyon-ui" } }).then((r) => r.json()),
    `${BASE}/${path}`,
  )

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
  const saved = await readItem(page, `workstreams/${id}`)
  // The PATCH carried the revision the item had before it (one less than now): the current one, not +1.
  expect(patch?.body).toEqual({ patch: { status: "paused" }, expected_revision: saved.rev - 1 })
  expect(saved.status).toBe("paused")
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

test("a refused write is reported as a toast, and nothing is saved", async ({ page }) => {
  const id = seedId("workstreams", 1)
  // The backend refuses this item's edits (registered after the stub, so it answers first).
  await page.route(`**/api/plugins/console/v1/planner/workstreams/${id}`, (route) =>
    route.request().method() === "PATCH"
      ? route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ code: "internal", message: "The planner store is unavailable." }),
        })
      : route.fallback(),
  )
  await page.goto(`/planner/streams/${id}`)
  // The title is an inline editor (contenteditable): type as a person does, then commit with Enter.
  await page
    .getByRole("region", { name: "Substrate migration" })
    .getByRole("textbox", { name: "Title" })
    .click()
  await page.keyboard.press("ControlOrMeta+a")
  await page.keyboard.type("Renamed by the browser suite")
  await page.keyboard.press("Enter")
  await expect(
    page.locator("[data-sonner-toast]").filter({ hasText: "The planner store is unavailable." }),
  ).toBeVisible()
  // Nothing was saved: the item still has its title (the editor keeps the typed text, as in the old build).
  const saved = await readItem(page, `workstreams/${id}`)
  expect(saved.title).toBe("Substrate migration")
  await shot(page, "planner-write-refused")
})
