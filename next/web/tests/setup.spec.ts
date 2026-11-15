import { expect, test } from "@playwright/test"
import { freshBackend } from "../e2e/env"
import { shot, signedOut } from "./helpers"

// A backend that never gets an owner (second server in playwright.config.ts).
test.use({ storageState: signedOut, baseURL: freshBackend })

test("no owner: a plain screen says how to create one, with no form", async ({ page }) => {
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "No owner yet" })).toBeVisible()
  await expect(page.getByText(/tachyon identity setup/)).toBeVisible()
  await expect(page.getByLabel("Username")).toHaveCount(0)
  await expect(page.getByLabel("Password")).toHaveCount(0)
  await shot(page, "no-owner")
})

test("the API reports setup is not offered over HTTP and refuses it", async ({ request }) => {
  const state = await (await request.get("/api/setup")).json()
  expect(state.setup_required).toBe(true)
  expect(state.setup_over_http).toBe(false)
  const r = await request.post("/api/setup", {
    headers: { "X-Requested-With": "tachyon-ui", Origin: freshBackend },
    data: { username: "intruder", display_name: "x", password: "a long enough passphrase" },
  })
  expect(r.status()).toBe(403)
  expect((await r.json()).code).toBe("setup_offline")
})
