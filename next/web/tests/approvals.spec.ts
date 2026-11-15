import { APPROVALS } from "../e2e/approvals-stub"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Approvals behind the real next/ binary's identity and shell, against the approvals contract stub
// (e2e/approvals-stub.ts), or with TACHYON_E2E_APPROVALS=backend against the binary's own approvals (B7).
test.use({ storageState: owner })

const queue = (page: import("@playwright/test").Page) =>
  page.getByRole("region", { name: "Pending approvals" })
const detail = (page: import("@playwright/test").Page) =>
  page.getByRole("region", { name: "Decision detail" })
const resolvePath = (id: string) => `${APPROVALS}/items/${id}/resolve`

test("the queue lists what needs a decision and opens the first", async ({ page }) => {
  await page.goto("/approvals")
  await expect(page.getByRole("tab", { name: /^Pending 3/ })).toBeVisible()
  for (const t of ["Drop the staging database", "Restart the indexer", "Provide the deploy key"])
    await expect(queue(page).getByRole("listitem", { name: new RegExp(t) })).toBeVisible()
  await expect(detail(page).getByRole("heading", { level: 2 })).toBeVisible()
  await shot(page, "approvals-queue")
})

test("approve: the decision carries the reviewed revisions and the item leaves the queue", async ({
  page,
}) => {
  await page.goto("/approvals")
  await queue(page)
    .getByRole("listitem", { name: /Restart the indexer/ })
    .click()
  await expect(detail(page).getByRole("heading", { name: "Restart the indexer" })).toBeVisible()
  await shot(page, "approvals-detail")
  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === resolvePath("req-e2e-low"),
  )
  await detail(page).getByRole("button", { name: "Approve", exact: true }).click()
  expect((await sent).postDataJSON()).toMatchObject({
    expected_revision: 1,
    presented_projection_revision: 1,
    response: { kind: "approval", decision: "approved" },
  })
  await expect(queue(page).getByRole("listitem", { name: /Restart the indexer/ })).toHaveCount(0)
})

test("a high-risk approval needs the server's phrase typed exactly", async ({ page }) => {
  await page.goto("/approvals")
  await queue(page)
    .getByRole("listitem", { name: /Drop the staging database/ })
    .click()
  const approve = detail(page).getByRole("button", { name: "Approve", exact: true })
  await expect(detail(page).getByText("Type APPROVE 1a2b3c4d to confirm")).toBeVisible()
  await expect(approve).toBeDisabled()
  await detail(page).getByRole("textbox", { name: "Confirmation phrase" }).fill("APPROVE 1a2b3c4d")
  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === resolvePath("req-e2e-high"),
  )
  await approve.click()
  // The phrase travels inside the response, as the server reads it.
  expect((await sent).postDataJSON()).toMatchObject({
    response: { decision: "approved", confirmation: "APPROVE 1a2b3c4d" },
  })
  await expect(
    queue(page).getByRole("listitem", { name: /Drop the staging database/ }),
  ).toHaveCount(0)
})

test("an operator refusal is shown as a message, held or immediate, and the request stays", async ({
  page,
  approvals,
}) => {
  test.skip(
    !approvals.store,
    "the backend run refuses with a principal that lacks the scope (see the PR)",
  )
  if (!approvals.store) return
  approvals.store.refuseOperator = true
  await page.goto("/approvals")
  // A high-risk approval is sent at once; the refusal shows in the decision bar.
  await queue(page)
    .getByRole("listitem", { name: /Drop the staging database/ })
    .click()
  await detail(page).getByRole("textbox", { name: "Confirmation phrase" }).fill("APPROVE 1a2b3c4d")
  await detail(page).getByRole("button", { name: "Approve", exact: true }).click()
  await expect(
    detail(page)
      .getByRole("alert")
      .filter({ hasText: "Deciding this request needs operator access." }),
  ).toBeVisible()
  // A low-risk approval is held for undo, then sent; its refusal is a toast (it used to flash and vanish).
  await queue(page)
    .getByRole("listitem", { name: /Restart the indexer/ })
    .click()
  await detail(page).getByRole("button", { name: "Approve", exact: true }).click()
  await expect(
    page
      .locator("[data-sonner-toast]")
      .filter({ hasText: "Deciding this request needs operator access." }),
  ).toBeVisible({ timeout: 15_000 })
  await expect(queue(page).getByRole("listitem", { name: /Restart the indexer/ })).toBeVisible()
  await shot(page, "approvals-operator-refused")
})

test("defer: the request is snoozed with a duration", async ({ page }) => {
  await page.goto("/approvals")
  await queue(page)
    .getByRole("listitem", { name: /Restart the indexer/ })
    .click()
  const sent = page.waitForRequest(
    (r) =>
      r.method() === "POST" && new URL(r.url()).pathname === `${APPROVALS}/items/req-e2e-low/defer`,
  )
  await detail(page).getByRole("button", { name: "Decide later" }).click()
  await page.getByRole("menuitem").first().click()
  const body = (await sent).postDataJSON()
  expect(typeof body.duration_s).toBe("number")
  expect(body.duration_s).toBeGreaterThan(0)
})

test("secret dialog: a multi-line value is stored, and it never appears in the page afterwards", async ({
  page,
}) => {
  // A synthetic two-line value, typed into the masked field.
  const typed = ["first-line-e2e-value", "second-line-e2e-value"].join("\n")
  await page.goto("/approvals")
  await queue(page)
    .getByRole("listitem", { name: /Provide the deploy key/ })
    .click()
  await detail(page)
    .getByRole("button", { name: /Enter securely/ })
    .click()
  const dialog = page.getByRole("dialog", { name: "Deploy key" })
  await dialog.getByRole("textbox", { name: "Secret value" }).fill(typed)
  await shot(page, "approvals-secret-dialog")
  const stored = page.waitForResponse(
    (r) => r.request().method() === "POST" && new URL(r.url()).pathname === `${APPROVALS}/secrets`,
  )
  await dialog.getByRole("button", { name: "Store secret" }).click()
  expect((await stored).ok()).toBe(true)
  await expect(dialog).toHaveCount(0)
  await expect(detail(page).getByText(/Stored/)).toBeVisible()
  const html = await page.content()
  expect(html).not.toContain("first-line-e2e-value")
  expect(html).not.toContain("second-line-e2e-value")
  const values = await page.evaluate(() =>
    [...document.querySelectorAll("input, textarea")].map((e) => (e as HTMLInputElement).value),
  )
  expect(values.join("\n")).not.toContain("-e2e-value")
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the queue fits and opens a request", async ({ page }) => {
    await page.goto("/approvals")
    await expect(queue(page).getByRole("listitem", { name: /Restart the indexer/ })).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "approvals-narrow")
    await queue(page)
      .getByRole("listitem", { name: /Restart the indexer/ })
      .click()
    await expect(detail(page).getByRole("heading", { name: "Restart the indexer" })).toBeVisible()
  })
})
