import type { Page } from "@playwright/test"
import { expect, test } from "./fixtures"
import { ownerName, ownerPassword, shot, signedOut, signIn, signInHeading } from "./helpers"

test.use({ storageState: signedOut })

const shellReady = (page: Page) =>
  expect(page.getByRole("link", { name: "Planner", exact: true })).toBeVisible()

async function signInFields(page: Page) {
  await page.getByLabel("Username").fill(ownerName)
  await page.getByLabel("Password").fill(ownerPassword())
  await page.getByRole("button", { name: "Sign in" }).click()
}

test("signed out shows sign-in and makes no Tachyon data call", async ({ page }) => {
  const calls: string[] = []
  page.on("request", (r) => {
    const { pathname } = new URL(r.url())
    if (pathname.startsWith("/api/")) calls.push(`${r.method()} ${pathname}`)
  })
  await page.goto("/planner")
  await expect(signInHeading(page)).toBeVisible()
  await shot(page, "sign-in")
  // Only the identity kit's own probes (setup and session) run.
  expect(calls.length).toBeGreaterThan(0)
  expect(calls.every((c) => /^GET \/api\/(setup|identity\/)/.test(c))).toBe(true)
})

test("a wrong password is shown on the form and keeps the sign-in screen", async ({ page }) => {
  await signIn(page, ownerName, "not the right password")
  await expect(page.getByRole("alert")).toContainText("invalid username or password")
  await expect(signInHeading(page)).toBeVisible()
})

test("sign-in opens the shell, a reload keeps the session, sign-out returns", async ({ page }) => {
  await signIn(page, ownerName, ownerPassword())
  await shellReady(page)
  await page.reload()
  await shellReady(page)
  await page.getByRole("link", { name: "Sign out" }).click()
  await page.getByRole("button", { name: "Sign out" }).click()
  await expect(signInHeading(page)).toBeVisible()
})

test("a signed-out deep link is kept through sign-in; an explicit sign-out goes home", async ({
  page,
}) => {
  await page.goto("/chat/thread-9")
  await expect(signInHeading(page)).toBeVisible()
  await signInFields(page)
  await shellReady(page)
  expect(new URL(page.url()).pathname).toBe("/chat/thread-9")
  await page.getByRole("link", { name: "Sign out" }).click()
  await page.getByRole("button", { name: "Sign out" }).click()
  await signInFields(page)
  await shellReady(page)
  // Home is the planner, which opens its themes.
  await expect(page).toHaveURL(/\/planner\/themes/)
})

test("an expired session returns to sign-in with the kit's notice", async ({ page }) => {
  await signIn(page, ownerName, ownerPassword())
  await shellReady(page)
  // The session ends while the tab is away; coming back reads the profile again and gets a 401.
  await page.context().clearCookies()
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")))
  await expect(page.getByText("Your session has ended")).toBeVisible()
  await expect(signInHeading(page)).toBeVisible()
})
