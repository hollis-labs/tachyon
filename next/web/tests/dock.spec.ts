import type { Locator, Page } from "@playwright/test"
import { expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// The chat dock (G8b): the carried overlay drawer above the shell, opened from the carried trigger (the host draws it
// outside the shell, src/chat-opener.tsx), against the chat contract stub (e2e/chat-stub.ts) or, with
// TACHYON_E2E_CHAT=backend, the binary's own chat.
test.use({ storageState: owner })

const SESSION = "tether:sess-e2e-1"
const messagesPath = `/api/plugins/console/v1/chat/threads/${encodeURIComponent(SESSION)}/messages`

const opener = (page: Page) => page.locator("[data-chat-trigger]")
const drawer = (page: Page) => page.getByRole("complementary", { name: "Chat drawer" })

/** Every visible shell or page control (and the page headers), other than the opener, that overlaps the opener. */
async function covered(page: Page) {
  const box = await opener(page).boundingBox()
  expect(box).not.toBeNull()
  return page.evaluate(
    (o) => {
      const hit: string[] = []
      for (const el of document.querySelectorAll(
        "a, button, input, textarea, select, [role=tab], [role=button], header, h1",
      )) {
        if (el.closest("[data-chat-opener]")) continue
        const r = el.getBoundingClientRect()
        if (r.width === 0 || r.height === 0 || getComputedStyle(el).visibility === "hidden")
          continue
        if (r.right > o.x && r.left < o.x + o.width && r.bottom > o.y && r.top < o.y + o.height)
          hit.push(
            `${el.tagName} ${el.getAttribute("aria-label") ?? el.textContent?.trim().slice(0, 30)}`,
          )
      }
      return hit
    },
    box as NonNullable<typeof box>,
  )
}

async function openConversation(dock: Locator) {
  await dock.getByText("Reviewer").first().click()
  await expect(dock.getByText("The planner surface is ready for review.")).toBeVisible()
}

test("the opener opens the dock over a non-chat page; a message sends; Esc closes it", async ({
  page,
}) => {
  await page.goto("/planner")
  await expect(page.getByRole("heading", { name: "Planner", level: 1 }).first()).toBeVisible()
  await page.waitForLoadState("networkidle")
  const under = new URL(page.url()).pathname
  await expect(drawer(page)).toHaveCount(0)
  await expect(opener(page)).toHaveAttribute("aria-expanded", "false")
  await opener(page).click()
  const dock = drawer(page)
  await expect(dock).toBeVisible()
  await expect(dock).toBeFocused()
  await expect(dock.getByRole("link", { name: /^Approvals/ })).toBeVisible()
  await openConversation(dock)
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
  await expect(drawer(page)).toHaveCount(0)
  await expect(opener(page)).toBeFocused()
})

test("a dialog inside the dock takes Escape first; the dock closes on the next one", async ({
  page,
}) => {
  await page.goto("/agents")
  await opener(page).click()
  const dock = drawer(page)
  await openConversation(dock)
  const message = dock
    .locator("article")
    .filter({ hasText: "The planner surface is ready for review." })
  await message.hover()
  await message.getByRole("button", { name: "More message actions" }).click()
  await page.getByRole("menuitem", { name: "Message details" }).click()
  const details = page.getByRole("dialog", { name: "Message details" })
  await expect(details).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(details).toHaveCount(0)
  await expect(dock).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(drawer(page)).toHaveCount(0)
})

test("the opener shows the unread count, and its Close button and the opener both close the dock", async ({
  page,
  chat,
}) => {
  test.skip(!chat.store, "the unread count is seeded in the stub")
  const store = chat.store as NonNullable<typeof chat.store>
  store.threads[1].unread = 3
  await page.goto("/planner")
  await expect(opener(page)).toHaveAccessibleName("Chat, 3 unread")
  await opener(page).click()
  await expect(opener(page)).toHaveAttribute("aria-expanded", "true")
  await drawer(page).getByRole("button", { name: "Close chat" }).click()
  await expect(drawer(page)).toHaveCount(0)
  await opener(page).click()
  await expect(drawer(page)).toBeVisible()
  await opener(page).click()
  await expect(drawer(page)).toHaveCount(0)
})

for (const { width, height } of [
  { width: 1440, height: 900 },
  { width: 1024, height: 700 },
  { width: 1023, height: 800 },
]) {
  test(`${width}x${height}: the opener covers no shell or page control`, async ({ page }) => {
    await page.setViewportSize({ width, height })
    for (const path of ["/planner", "/chat", "/agents"]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      await expect(opener(page)).toBeVisible()
      expect(await covered(page), path).toEqual([])
    }
  })
}

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the opener clears the header and the tab bar, and steps aside for the More sheet", async ({
    page,
  }) => {
    for (const path of ["/planner", "/agents"]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      expect(await covered(page), path).toEqual([])
    }
    await page.getByRole("button", { name: "More", exact: true }).click()
    const sheet = page.getByRole("dialog", { name: "More" })
    await expect(sheet).toBeVisible()
    await expect(opener(page)).toHaveCount(0)
    await shot(page, "dock-opener-more")
    await page.keyboard.press("Escape")
    await expect(sheet).toHaveCount(0)
    await expect(opener(page)).toBeVisible()
    await shot(page, "dock-opener-narrow")

    await opener(page).click()
    await expect(drawer(page).getByText("Reviewer").first()).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(drawer(page)).toHaveCount(0)
  })
})
