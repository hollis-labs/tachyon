import type { Page } from "@playwright/test"
import { AGENTS } from "../e2e/agents-stub"
import { agentsBackend, expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Agent Ops and Needs you behind the real next/ binary's identity and shell, against the Agent Ops contract stub
// (e2e/agents-stub.ts, the old build's fixtures), or with TACHYON_E2E_AGENTS=backend against the binary's own agents
// reads (B8a), seeded by e2e/agents-seed.sql with the same names. Counts and clock times differ between the two.
test.use({ storageState: owner })

const session = (page: Page) => page.getByRole("dialog", { name: "Chat Backend Builder" })

test("the agents list shows the live sessions, grouped by workspace", async ({ page }) => {
  await page.goto("/agents")
  await expect(page.getByRole("tab", { name: /^Sessions \d+/ })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  const tachyon = page.getByRole("region", { name: "tachyon" })
  await expect(
    tachyon.getByRole("listitem", { name: "Chat Backend Builder, Working" }),
  ).toBeVisible()
  await expect(tachyon.getByRole("listitem", { name: "Reviewer, Needs approval" })).toBeVisible()
  await expect(
    page.getByRole("progressbar", {
      name: agentsBackend ? /1 working, 1 waiting/ : /2 working, 2 waiting/,
    }),
  ).toBeVisible()
  await shot(page, "agents-list")
})

test("a session opens by deep link, and its timeline shows the events by day", async ({ page }) => {
  await page.goto("/agents/sess_work")
  await expect(session(page).getByRole("heading", { name: "Chat Backend Builder" })).toBeVisible()
  await session(page).getByRole("tab", { name: "Timeline" }).click()
  await expect(
    session(page)
      .getByText(/Session started \(claude, claude-sonnet-5-5\)/)
      .first(),
  ).toBeVisible()
  await expect(
    session(page)
      .getByText(/Tachyon was offline \d\d:\d\d–\d\d:\d\d/)
      .first(),
  ).toBeVisible()
  await shot(page, "agents-timeline")
  await session(page).getByRole("button", { name: "Close" }).click()
  await expect(page).toHaveURL(/\/agents$/)
})

test("seats: each seat's declared status beside its live session", async ({ page }) => {
  await page.goto("/agents")
  await page.getByRole("tab", { name: "Seats" }).click()
  const seats = page.getByRole("list", { name: "Seats" })
  const builder = seats.getByRole("listitem").filter({ hasText: "@builder-1" })
  await expect(builder).toContainText("needs the deploy token")
  await expect(builder).toContainText("waiting on @chrispian")
  await expect(builder.getByRole("link", { name: /^session (working|waiting)$/ })).toHaveAttribute(
    "href",
    "/agents/sess_work",
  )
  await expect(seats.getByRole("listitem").filter({ hasText: "@reviewer" })).toContainText(
    "no live session",
  )
  await shot(page, "agents-seats")
})

test("a refused nudge shows the backend's message in the panel, and the text stays", async ({
  page,
  agents,
}) => {
  // Stub: the stub refuses the control. Backend: B8b sends the nudge as Tether mail, and the run's Tether fake
  // refuses a message containing REFUSE (e2e/tether-fake.ts).
  if (agents.store) agents.store.refuseControls = true
  const text = agentsBackend
    ? "REFUSE: please rebase on the latest main."
    : "Please rebase on the latest main."
  const refusal = agentsBackend
    ? "Tether could not deliver the message."
    : "The session is not at a turn boundary. Try again when it is."
  await page.goto("/agents/sess_work")
  const sent = page.waitForRequest(
    (r) =>
      r.method() === "POST" && new URL(r.url()).pathname === `${AGENTS}/sessions/sess_work/nudge`,
  )
  await session(page)
    .getByRole("button", { name: /^Nudge/ })
    .click()
  const box = page.getByRole("textbox", { name: "Nudge or steer" })
  await box.fill(text)
  await page.getByRole("button", { name: /^Send/ }).click()
  expect((await sent).postDataJSON()).toMatchObject({ text })
  // The old panel shows a control's refusal inline (role alert), not as a toast; carried as is.
  await expect(page.getByRole("alert").filter({ hasText: refusal })).toBeVisible()
  await expect(box).toHaveValue(text)
  await shot(page, "agents-nudge-refused")
})

test("ceremony: the week's gates, waits and stalls", async ({ page }) => {
  // The meter reads Torque, which the backend run does not configure (its source reports not configured).
  test.skip(agentsBackend, "needs a configured Torque")
  await page.goto("/agents")
  await page.getByRole("tab", { name: "Ceremony" }).click()
  await expect(page.getByText("Port the seats view")).toBeVisible()
  await expect(page.getByText("420 / 6")).toBeVisible()
})

test("needs you: what waits on the operator, each with where to act", async ({ page }) => {
  await page.goto("/needs-you")
  const list = page.getByRole("list", { name: "Needs you" })
  await expect(
    list.getByRole("listitem").filter({ hasText: "needs the deploy token" }),
  ).toContainText("Blocked on you")
  // The stub also lists the approvals queue; the backend run seeds no approvals.
  if (!agentsBackend)
    await expect(list.getByRole("link", { name: "Approvals waiting" })).toHaveAttribute(
      "href",
      "/approvals",
    )
  await shot(page, "needs-you")
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the agents list fits and opens a session", async ({ page }) => {
    await page.goto("/agents")
    const row = page.getByRole("listitem", { name: "Chat Backend Builder, Working" })
    await expect(row).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "agents-narrow")
    await row.click()
    await expect(session(page).getByRole("heading", { name: "Chat Backend Builder" })).toBeVisible()
  })
})
