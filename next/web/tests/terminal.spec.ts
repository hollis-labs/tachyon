import type { Page } from "@playwright/test"
import { TERMINAL } from "../e2e/terminal-stub"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// The chat drawer's PTY terminal behind the real next/ binary's identity and shell, against the terminal contract stub
// (e2e/terminal-stub.ts: REST routes plus a fake PTY WebSocket that echoes input). The terminal backend is not built
// yet; TACHYON_E2E_TERMINAL=backend turns the stub off once it is.
test.use({ storageState: owner })

const ROW = "chat://tether/launch/L_e2e"
const openTerminal = async (page: Page) => {
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  await page.getByRole("button", { name: "Terminal", exact: true }).click()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeVisible()
}
const status = (page: Page) => page.getByRole("status").filter({ hasText: /^ssh · saved key/ })

test("connect, type and see the echo, resize, then disconnect", async ({ page, terminal }) => {
  test.skip(!terminal.store, "the stub's PTY echo")
  if (!terminal.store) return
  const store = terminal.store
  await openTerminal(page)
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(status(page)).toHaveText("ssh · saved key · open")
  const term = page.getByRole("group", { name: "Terminal session" })
  await term.locator("textarea").focus() // xterm's input element
  await page.keyboard.type("echo port-ok")
  await expect(term.locator(".xterm-rows")).toContainText("echo port-ok")
  await expect.poll(() => store.resizes.length).toBeGreaterThan(0)
  const before = store.resizes.length
  await page.setViewportSize({ width: 1000, height: 700 })
  await expect.poll(() => store.resizes.length).toBeGreaterThan(before)
  // Below 1100px the Agent Panel overlays the conversation (carried layout); back to the default width to reach the bar.
  await page.setViewportSize({ width: 1280, height: 720 })
  await shot(page, "terminal")
  await page.getByRole("button", { name: "Disconnect", exact: true }).click()
  await expect(term).toHaveCount(0)
  await expect.poll(() => store.closed.length).toBe(1)
})

test("a refused session shows the backend's message", async ({ page, terminal }) => {
  test.skip(!terminal.store, "the stub's refusal")
  if (!terminal.store) return
  terminal.store.refuseCreate = true
  await openTerminal(page)
  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === `${TERMINAL}/sessions`,
  )
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await sent
  await expect(
    page.getByRole("alert").filter({ hasText: "Opening a terminal needs operator access." }),
  ).toBeVisible()
  await shot(page, "terminal-refused")
})

test("a refused upgrade closes the session and says so", async ({ page, terminal }) => {
  test.skip(!terminal.store, "the stub's refusal")
  if (!terminal.store) return
  terminal.store.refuseUpgrade = true
  await openTerminal(page)
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(status(page)).toHaveText("ssh · saved key · disconnected")
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the terminal opens and fits", async ({ page }) => {
    await openTerminal(page)
    await page.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page.getByRole("group", { name: "Terminal session" })).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "terminal-narrow")
  })
})
