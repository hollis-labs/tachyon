import type { Page } from "@playwright/test"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// The chat dock (G8b): an app-shell dock.tab opened from the header toolbar, against the chat contract stub
// (e2e/chat-stub.ts) or, with TACHYON_E2E_CHAT=backend, the binary's own chat.
test.use({ storageState: owner })

const SESSION = "tether:sess-e2e-1"
const messagesPath = `/api/plugins/console/v1/chat/threads/${encodeURIComponent(SESSION)}/messages`

const toolbarChat = (page: Page) =>
  page.getByRole("toolbar", { name: "Primary actions" }).getByRole("button", { name: "Chat" })
const chatDock = (page: Page) => page.getByRole("dialog", { name: "Chat" })

test("the toolbar opens the dock on a non-chat page; a message sends; Esc closes it", async ({
  page,
}) => {
  await page.goto("/planner")
  await expect(page.getByRole("heading", { name: "Planner", level: 1 }).first()).toBeVisible()
  await page.waitForLoadState("networkidle")
  const under = new URL(page.url()).pathname
  await expect(chatDock(page)).toHaveCount(0)
  await toolbarChat(page).click()
  const dock = chatDock(page)
  await expect(dock).toBeVisible()
  await expect(dock).toHaveAttribute("data-presentation", "side")
  await expect(dock.getByRole("link", { name: /^Approvals/ })).toBeVisible()
  await dock.getByText("Reviewer").first().click()
  await expect(dock.getByText("The planner surface is ready for review.")).toBeVisible()
  // The page under the dock stays where it was.
  expect(new URL(page.url()).pathname).toBe(under)

  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === messagesPath,
  )
  await dock.getByRole("textbox", { name: "Message", exact: true }).fill("Sent from the dock.")
  await dock.getByRole("button", { name: "Send", exact: true }).click()
  expect((await sent).postDataJSON()).toMatchObject({ text: "Sent from the dock." })
  await expect(dock.getByText("Sent from the dock.")).toBeVisible()
  await shot(page, "dock")

  await page.keyboard.press("Escape")
  await expect(chatDock(page)).toHaveCount(0)
  await expect(toolbarChat(page)).toBeFocused()
})

test("the dock's size pick is remembered, and its Close button closes it", async ({ page }) => {
  await page.goto("/agents")
  await toolbarChat(page).click()
  const dock = chatDock(page)
  await expect(dock.getByRole("button", { name: "Size lg" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await dock.getByRole("button", { name: "Size md" }).click()
  await expect(dock.getByRole("button", { name: "Size md" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await dock.getByRole("button", { name: "Close", exact: true }).click()
  await expect(chatDock(page)).toHaveCount(0)
  await page.reload()
  await toolbarChat(page).click()
  await expect(chatDock(page).getByRole("button", { name: "Size md" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the dock opens as a bottom sheet and does not overflow", async ({ page }) => {
    await page.goto("/planner")
    await toolbarChat(page).click()
    const dock = chatDock(page)
    await expect(dock).toHaveAttribute("data-presentation", "bottom-sheet")
    await expect(dock.getByText("Reviewer").first()).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    const box = await dock.boundingBox()
    expect(box && box.x + box.width).toBeLessThanOrEqual(390)
    await shot(page, "dock-narrow")
    await page.keyboard.press("Escape")
    await expect(chatDock(page)).toHaveCount(0)
  })
})
