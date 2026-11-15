import type { Page } from "@playwright/test"
import { FORGE } from "../e2e/forge-stub"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Repos (the PR inbox and the PR workspace) behind the real next/ binary's identity and shell, against the forge
// contract stub (e2e/forge-stub.ts, the old build's fixtures). The forge backend is not built yet;
// TACHYON_E2E_FORGE=backend turns the stub off once it is.
test.use({ storageState: owner })

const inbox = (page: Page) => page.getByRole("region", { name: "Pending pull requests" })
const PR = "/pr/github/hollis-labs/tachyon/201"

test("the PR inbox groups what needs attention", async ({ page }) => {
  await page.goto("/repos")
  await expect(inbox(page).getByRole("tab", { name: /^Active 8/ })).toBeVisible()
  await expect(inbox(page).getByRole("group", { name: "Failing checks" })).toBeVisible()
  await expect(
    inbox(page).getByRole("button", { name: /^Preview hollis-labs\/tachyon#201/ }),
  ).toBeVisible()
  await inbox(page)
    .getByRole("tab", { name: /^Needs review/ })
    .click()
  await expect(inbox(page).getByRole("group", { name: "Failing checks" })).toHaveCount(0)
  await shot(page, "repos-inbox")
})

test("a pull request opens in the workspace by deep link, with its files and conversation", async ({
  page,
}) => {
  await page.goto(PR)
  await expect(
    page.getByRole("heading", { name: /approvals: A1 queue index #201/, level: 1 }),
  ).toBeVisible()
  await expect(page.getByRole("tab", { name: /^Files changed/ })).toBeVisible()
  await shot(page, "repos-workspace")
  await page.getByRole("tab", { name: /^Conversation/ }).click()
  await expect(page.getByRole("textbox", { name: "Add a comment…" })).toBeVisible()
})

test("a refused comment shows the backend's message, and the draft stays", async ({
  page,
  forge,
}) => {
  test.skip(!forge.store, "the stub's refusal")
  if (!forge.store) return
  forge.store.refuseComments = true
  await page.goto(PR)
  await page.getByRole("tab", { name: /^Conversation/ }).click()
  const box = page.getByRole("textbox", { name: "Add a comment…" })
  await box.fill("Please split the index change.")
  const sent = page.waitForRequest(
    (r) =>
      r.method() === "POST" &&
      new URL(r.url()).pathname === `${FORGE}/repos/github/hollis-labs/tachyon/pulls/201/comments`,
  )
  await page.getByRole("button", { name: "Comment", exact: true }).click()
  await sent
  await expect(
    page
      .getByText("The pull request moved on since you opened it. Refresh and comment again.")
      .first(),
  ).toBeVisible()
  await expect(box).toHaveValue("Please split the index change.")
  await shot(page, "repos-comment-refused")
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the PR inbox fits", async ({ page }) => {
    await page.goto("/repos")
    await expect(
      inbox(page).getByRole("button", { name: /^Preview hollis-labs\/tachyon#201/ }),
    ).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "repos-narrow")
  })
})
