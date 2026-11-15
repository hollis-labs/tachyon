import { chatBackend, expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Chat behind the real next/ binary's identity and shell, against the chat contract stub (e2e/chat-stub.ts), or
// with TACHYON_E2E_CHAT=backend against the binary's own chat (B6).
test.use({ storageState: owner })

/** The roster row's thread id (one row per agent; the API sessions attach to it). */
const ROW = "chat://tether/launch/L_e2e"
const SESSION = "tether:sess-e2e-1"
const messagesPath = `/api/plugins/console/v1/chat/threads/${encodeURIComponent(SESSION)}/messages`

test("the conversation list opens the newest conversation with its messages", async ({ page }) => {
  await page.goto("/chat")
  await expect(page.getByRole("link", { name: "Chat", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(page).toHaveURL(new RegExp(`/chat/${encodeURIComponent(ROW).replace(/%/g, "%")}$`))
  const conversation = page.getByRole("region", { name: "Conversation" }).first()
  await expect(conversation.getByText("The planner surface is ready for review.")).toBeVisible()
  await expect(conversation.getByText("How is the port going?")).toBeVisible()
  await shot(page, "chat-list")
})

test("a conversation opens by deep link, and a reload keeps it", async ({ page }) => {
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  const conversation = page.getByRole("region", { name: "Conversation" }).first()
  await expect(conversation.getByText("The planner surface is ready for review.")).toBeVisible()
  await page.reload()
  await expect(conversation.getByText("The planner surface is ready for review.")).toBeVisible()
  await shot(page, "chat-thread")
})

test("send: the message goes to the session and shows in the conversation", async ({ page }) => {
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === messagesPath,
  )
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Please review the chat port next.")
  await page.getByRole("button", { name: "Send", exact: true }).click()
  expect((await sent).postDataJSON()).toMatchObject({ text: "Please review the chat port next." })
  await expect(
    page
      .getByRole("region", { name: "Conversation" })
      .first()
      .getByText("Please review the chat port next."),
  ).toBeVisible()
})

test("a refused send shows the backend's message and keeps the draft", async ({ page }) => {
  await page.route(`**${messagesPath}`, (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            code: "delivery_unknown",
            message: "The agent's session is not reachable right now.",
          }),
        })
      : route.fallback(),
  )
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  const box = page.getByRole("textbox", { name: "Message", exact: true })
  await box.fill("This one will not go through.")
  await page.getByRole("button", { name: "Send", exact: true }).click()
  await expect(
    page.getByRole("alert").filter({ hasText: "The agent's session is not reachable right now." }),
  ).toBeVisible()
  await expect(box).toHaveValue("This one will not go through.")
  await shot(page, "chat-send-refused")
})

test("plans tab: the plan and its steps show, and a new step is added against the plan's revision", async ({
  page,
}) => {
  const listed = page.waitForResponse(
    (r) => r.request().method() === "GET" && new URL(r.url()).pathname.endsWith("/plans"),
  )
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  const panel = page.getByRole("complementary", { name: "Agent Panel" })
  await panel.getByRole("tab", { name: /^Plan/ }).click()
  const { plans } = (await (await listed).json()) as {
    plans: { title: string; revision: number }[]
  }
  const revision = plans.find((p) => p.title === "Port the chat surface")?.revision
  const plan = panel.getByRole("region", { name: "Port the chat surface" })
  await expect(plan.getByText("Carry the chat code")).toBeVisible()
  await expect(plan.getByText("Write the contract stub")).toBeVisible()
  const added = page.waitForRequest(
    (r) => r.method() === "POST" && /\/plans\/[^/]+\/steps$/.test(new URL(r.url()).pathname),
  )
  await plan.getByRole("button", { name: /Add a step/ }).click()
  await page.keyboard.type("Run the real-backend e2e")
  await page.keyboard.press("Enter")
  expect((await added).postDataJSON()).toMatchObject({
    title: "Run the real-backend e2e",
    expected_revision: revision,
  })
  expect(revision).toBeGreaterThan(0)
  await expect(plan.getByText("Run the real-backend e2e")).toBeVisible()
})

test("artifacts tab: the agent's shared file is listed and opens", async ({ page }) => {
  // A rendering check. The old build never writes an agent-authored workspace item; the backend run marks its
  // uploaded file as the agent's in a fixture row (e2e/global-setup.ts).
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  const panel = page.getByRole("complementary", { name: "Agent Panel" })
  await panel.getByRole("button", { name: "Scroll tabs right" }).click()
  await panel.getByRole("tab", { name: "Artifacts" }).click()
  await expect(panel.getByText("port-notes.md")).toBeVisible()
  await panel.getByText("port-notes.md").click()
  await expect(page.getByText("Carried, not redesigned.")).toBeVisible()
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the conversation list is the landing view and opens a conversation", async ({
    page,
  }) => {
    await page.goto("/chat")
    await expect(page.getByText("Reviewer").first()).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "chat-narrow")
    await page.getByText("Reviewer").first().click()
    await expect(page.getByText("The planner surface is ready for review.")).toBeVisible()
  })
})

test("an unknown thread's topics are the backend's flat not_found, which the panel reads as empty", async ({
  page,
}) => {
  test.skip(!chatBackend, "the backend's own error body")
  const r = await page.request.get(
    `/api/plugins/console/v1/chat/threads/${encodeURIComponent("tether:no-such-session")}/topics`,
  )
  expect(r.status()).toBe(404)
  expect(await r.json()).toEqual({ code: "not_found", message: "No such thread." })
})
