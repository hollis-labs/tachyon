// Invoked by the opt-in Go TestNativeGateBrowser lane. The Go test owns two
// isolated issuers/databases and the HTTP server, enqueues through the producer
// API and verifies the actual immutable outcome after this real UI interaction.

import assert from "node:assert/strict"
import { chromium } from "@playwright/test"

const base = new URL(process.env.NATIVE_TEST_BASE)
assert.equal(base.hostname, "127.0.0.1")
const browser = await chromium.launch({
  channel: process.env.PLAYWRIGHT_CHANNEL ?? "chrome",
  headless: true,
})
try {
  const context = await browser.newContext()
  const login = await context.request.post(`${base.origin}/api/login`, {
    headers: { "X-Requested-With": "XMLHttpRequest" },
    data: { username: "owner", password: process.env.NATIVE_TEST_PASSWORD },
  })
  assert.equal(login.status(), 200)
  const page = await context.newPage()
  await page.goto(`${base.origin}/approvals`)
  if (process.env.NATIVE_TEST_QUARANTINED === "1") {
    const alert = page.getByRole("alert").filter({ hasText: "require reconciliation" })
    await alert.waitFor({ state: "visible" })
    assert.match(await alert.textContent(), /No approval evidence is available/)
    await page
      .getByRole("listitem", { name: /Legacy card remains usable/ })
      .waitFor({ state: "visible" })
    assert.equal(await page.getByRole("region", { name: "Native gate choice" }).count(), 0)
    assert.equal(await page.getByRole("button", { name: "Record exact choice" }).count(), 0)
  } else {
    const native = page.getByRole("region", { name: "Native gate choice" })
    await native.waitFor({ state: "visible" })
    const choice = native.getByRole("radio", { name: "allow · native-allow-once · Once" })
    await choice.waitFor({ state: "visible" })
    assert.equal(await choice.isChecked(), false)
    const submit = native.getByRole("button", { name: "Record exact choice" })
    assert.equal(await submit.isDisabled(), true)
    await choice.check()
    assert.equal(await submit.isEnabled(), true)
    const response = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/items/${process.env.NATIVE_TEST_ITEM}/resolve`) &&
        r.request().method() === "POST",
    )
    await submit.click()
    assert.equal((await response).status(), 200)
    assert.equal(await page.getByRole("button", { name: /^Undo$/ }).count(), 0)
  }
  await context.close()
} finally {
  await browser.close()
}
