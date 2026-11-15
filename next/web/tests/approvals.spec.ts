import type { Page } from "@playwright/test"
import { APPROVALS } from "../e2e/approvals-stub"
import { backend } from "../e2e/env"
import { approvalsBackend, expect, test } from "./fixtures"
import { owner, shot, signedOut } from "./helpers"

// Approvals behind the real next/ binary's identity and shell, against the approvals contract stub
// (e2e/approvals-stub.ts), or with TACHYON_E2E_APPROVALS=backend against the binary's own approvals (B7),
// seeded with e2e/approvals-seed.json. The backend keeps its state across tests, so the tests that decide
// a request come last, and defer is undone.
test.use({ storageState: owner })

const queue = (page: Page) => page.getByRole("region", { name: "Pending approvals" })
const detail = (page: Page) => page.getByRole("region", { name: "Decision detail" })
const STUB_IDS: Record<string, string> = {
  "Restart the indexer": "req-e2e-low",
  "Drop the staging database": "req-e2e-high",
  "Provide the deploy key": "req-e2e-form",
}

/** A pending request's id: the stub's fixed id, or the backend's, looked up by title. */
async function itemId(page: Page, title: string): Promise<string> {
  if (!approvalsBackend) return STUB_IDS[title]
  const r = await page.request.get(`${APPROVALS}/inbox`)
  const { pending } = (await r.json()) as {
    pending: { item_id: string; request_snapshot: { title: string } }[]
  }
  const it = pending.find((i) => i.request_snapshot.title === title)
  if (!it) throw new Error(`no pending request titled ${title}`)
  return it.item_id
}

const confirmText = (page: Page) =>
  detail(page).getByText(/^Type APPROVE [0-9A-Fa-f]{8} to confirm$/)

test("the queue lists what needs a decision and opens the first", async ({ page }) => {
  await page.goto("/approvals")
  await expect(page.getByRole("tab", { name: /^Pending 3/ })).toBeVisible()
  for (const t of ["Drop the staging database", "Restart the indexer", "Provide the deploy key"])
    await expect(queue(page).getByRole("listitem", { name: new RegExp(t) })).toBeVisible()
  await expect(detail(page).getByRole("heading", { level: 2 })).toBeVisible()
  await shot(page, "approvals-queue")
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

test("defer: the request is snoozed with a duration", async ({ page }) => {
  await page.goto("/approvals")
  const id = await itemId(page, "Restart the indexer")
  await queue(page)
    .getByRole("listitem", { name: /Restart the indexer/ })
    .click()
  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === `${APPROVALS}/items/${id}/defer`,
  )
  const deferred = page.waitForResponse(
    (r) =>
      r.request().method() === "POST" &&
      new URL(r.url()).pathname === `${APPROVALS}/items/${id}/defer`,
  )
  await detail(page).getByRole("button", { name: "Decide later" }).click()
  await page.getByRole("menuitem").first().click()
  const body = (await sent).postDataJSON()
  expect(typeof body.duration_s).toBe("number")
  expect(body.duration_s).toBeGreaterThan(0)
  expect((await deferred).ok()).toBe(true)
  if (approvalsBackend) {
    // The backend keeps the snooze: undo it for the tests after this one.
    const undone = await page.request.delete(`${APPROVALS}/items/${id}/defer`, {
      headers: { "X-Requested-With": "tachyon-ui" },
    })
    expect(undone.ok()).toBe(true)
  }
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

test("an operator refusal is shown as a message, held or immediate, and the request stays", async ({
  page,
  approvals,
}) => {
  test.skip(!approvals.store, "the backend run has its own refusal test below")
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

test("backend: a signed-in person without tachyon.operator is refused a high-risk decision, and the request stays", async ({
  page,
  browser,
}) => {
  test.skip(!approvalsBackend, "the backend's own refusal")
  // The owner's session mints a token for itself without "*": the same person, below operator.
  const minted = await page.request.post("/api/token", {
    headers: { "X-Requested-With": "tachyon-ui" },
    data: { scopes: ["tachyon.approvals.read", "tachyon.approvals.resolve"], ttl_seconds: 600 },
  })
  expect(minted.ok()).toBe(true)
  const { token } = (await minted.json()) as { token: string }
  const context = await browser.newContext({
    baseURL: backend,
    storageState: signedOut,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  })
  const person = await context.newPage()
  const id = await itemId(page, "Drop the staging database")
  // The server's own refusal body.
  const refused = await person.request.post(`${APPROVALS}/items/${id}/resolve`, {
    headers: { "X-Requested-With": "tachyon-ui" },
    data: {
      expected_revision: 1,
      presented_projection_revision: 1,
      response: { kind: "approval", decision: "approved" },
    },
  })
  expect(refused.status()).toBe(403)
  expect(await refused.json()).toMatchObject({
    code: "operator_required",
    message:
      "This action needs a signed-in person holding tachyon.operator; an API key never qualifies.",
  })
  // And in the UI: the decision bar shows the refusal, and the request stays.
  await person.goto("/approvals")
  await queue(person)
    .getByRole("listitem", { name: /Drop the staging database/ })
    .click()
  const phrase = ((await confirmText(person).textContent()) ?? "").replace(
    /^Type (.*) to confirm$/,
    "$1",
  )
  await detail(person).getByRole("textbox", { name: "Confirmation phrase" }).fill(phrase)
  await detail(person).getByRole("button", { name: "Approve", exact: true }).click()
  await expect(
    detail(person)
      .getByRole("alert")
      .filter({ hasText: "Deciding this request needs operator access." }),
  ).toBeVisible()
  await expect(
    queue(person).getByRole("listitem", { name: /Drop the staging database/ }),
  ).toBeVisible()
  await shot(person, "approvals-operator-refused")
  await context.close()
})

test("a high-risk approval needs the server's phrase typed exactly", async ({ page }) => {
  await page.goto("/approvals")
  const id = await itemId(page, "Drop the staging database")
  await queue(page)
    .getByRole("listitem", { name: /Drop the staging database/ })
    .click()
  const approve = detail(page).getByRole("button", { name: "Approve", exact: true })
  await expect(confirmText(page)).toBeVisible()
  const phrase = ((await confirmText(page).textContent()) ?? "").replace(
    /^Type (.*) to confirm$/,
    "$1",
  )
  if (!approvalsBackend) expect(phrase).toBe("APPROVE 1a2b3c4d")
  await expect(approve).toBeDisabled()
  await detail(page).getByRole("textbox", { name: "Confirmation phrase" }).fill(phrase)
  const sent = page.waitForRequest(
    (r) =>
      r.method() === "POST" && new URL(r.url()).pathname === `${APPROVALS}/items/${id}/resolve`,
  )
  await approve.click()
  // The phrase travels inside the response, as the server reads it.
  expect((await sent).postDataJSON()).toMatchObject({
    response: { decision: "approved", confirmation: phrase },
  })
  await expect(
    queue(page).getByRole("listitem", { name: /Drop the staging database/ }),
  ).toHaveCount(0)
})

test("approve: the decision carries the reviewed revisions and the item leaves the queue", async ({
  page,
}) => {
  await page.goto("/approvals")
  const id = await itemId(page, "Restart the indexer")
  await queue(page)
    .getByRole("listitem", { name: /Restart the indexer/ })
    .click()
  await expect(detail(page).getByRole("heading", { name: "Restart the indexer" })).toBeVisible()
  await shot(page, "approvals-detail")
  const sent = page.waitForRequest(
    (r) =>
      r.method() === "POST" && new URL(r.url()).pathname === `${APPROVALS}/items/${id}/resolve`,
  )
  await detail(page).getByRole("button", { name: "Approve", exact: true }).click()
  expect((await sent).postDataJSON()).toMatchObject({
    expected_revision: approvalsBackend ? expect.any(Number) : 1,
    presented_projection_revision: approvalsBackend ? expect.any(Number) : 1,
    response: { kind: "approval", decision: "approved" },
  })
  await expect(queue(page).getByRole("listitem", { name: /Restart the indexer/ })).toHaveCount(0)
})
