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

// The old server never served the identity seam's step-up (POST auth/step-up), so the confirm-gated writes were
// refused in the old app. The port keeps that (lead decision, parity): each is refused with the seam's message and
// nothing is sent to the forge.
const UNAVAILABLE = /Identity confirmation unavailable/
const forgeWrites = (page: Page) => {
  const sent: string[] = []
  page.on("request", (r) => {
    if (r.method() !== "GET" && new URL(r.url()).pathname.startsWith(`${FORGE}/repos/`))
      sent.push(r.url())
  })
  return sent
}

test("parity: a PR comment is refused (no step-up), and the draft stays", async ({ page }) => {
  const sent = forgeWrites(page)
  await page.goto(PR)
  await page.getByRole("tab", { name: /^Conversation/ }).click()
  const box = page.getByRole("textbox", { name: "Add a comment…" })
  await box.fill("Please split the index change.")
  await page.getByRole("button", { name: "Comment", exact: true }).click()
  await expect(page.getByRole("alert").filter({ hasText: UNAVAILABLE })).toBeVisible()
  await expect(box).toHaveValue("Please split the index change.")
  expect(sent).toEqual([])
  await shot(page, "repos-comment-refused")
})

test("parity: a review is refused (no step-up)", async ({ page }) => {
  const sent = forgeWrites(page)
  await page.goto(PR)
  await page.getByRole("button", { name: /^Review/ }).click()
  const review = page.getByRole("dialog", { name: "Review" })
  await review.getByRole("textbox", { name: "Review summary" }).fill("Looks close; see the note.")
  await review.getByRole("button", { name: "Submit review" }).click()
  await expect(page.getByText(UNAVAILABLE).first()).toBeVisible()
  expect(sent.filter((u) => u.endsWith("/reviews"))).toEqual([])
})

test("parity: Assign is refused (no step-up)", async ({ page }) => {
  const sent = forgeWrites(page)
  await page.goto("/repos")
  await inbox(page)
    .getByRole("button", { name: "Assign", exact: true })
    .first()
    .click({ force: true })
  const dialog = page.getByRole("dialog", { name: "Assign to an agent" })
  await dialog.getByRole("radio", { name: /Builder Reviewer/ }).click()
  await dialog.getByRole("button", { name: "Assign", exact: true }).click()
  await expect(page.getByText(UNAVAILABLE).first()).toBeVisible()
  expect(sent).toEqual([])
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
