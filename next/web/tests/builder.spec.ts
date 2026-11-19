import { builderBackend, expect, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Builder, Registry and the Agent Ops Teams tab behind the real next/ binary's identity and shell, against the
// builder contract stub (e2e/builder-stub.ts, the old build's fixtures). TACHYON_E2E_BUILDER=backend turns the stub
// off: the binary serves everything, its catalog index and team operations reading the local Tether fake
// (e2e/tether-fake.ts), with the teams and the formed run from e2e/builder-seed.sql.
test.use({ storageState: owner })

test("builder agents: the list, and an agent by deep link", async ({ page }) => {
  await page.goto("/builder")
  await expect(page).toHaveURL(/\/builder\/agents/)
  const list = page.getByRole("list", { name: "Agents" })
  await expect(list.getByRole("button", { name: /^Builder Reviewer/ })).toBeVisible()
  await expect(list.getByRole("button", { name: /^Release Captain/ })).toBeVisible()
  await page.goto("/builder/agents/release-captain")
  await expect(page.getByRole("heading", { name: "Release Captain", level: 2 })).toBeVisible()
  await shot(page, "builder-agents")
})

test("builder launches and library", async ({ page }) => {
  await page.goto("/builder/launches")
  await expect(
    page
      .getByRole("table", { name: "Launches" })
      .getByRole("row", { name: /builder-reviewer-tachyon/ }),
  ).toBeVisible()
  await shot(page, "builder-launches")
  await page.goto("/builder/library")
  await expect(
    page
      .getByRole("list", { name: "Library items" })
      .getByRole("button", { name: /^Review etiquette/ }),
  ).toBeVisible()
  await page.getByRole("link", { name: /^Skills/ }).click()
  await expect(page).toHaveURL(/\/builder\/library\/skills/)
  await shot(page, "builder-library")
})

test("builder teams: the saved team definitions", async ({ page }) => {
  await page.goto("/builder/teams")
  const teams = page.getByRole("list", { name: "Tachyon teams" })
  await expect(teams.getByRole("button", { name: /^Delivery/ })).toBeVisible()
  await expect(teams.getByRole("button", { name: /^Docs/ })).toBeVisible()
  await expect(page.getByRole("alert")).toHaveCount(0)
  await shot(page, "builder-teams")
})

test("registry: a project by deep link", async ({ page }) => {
  await page.goto("/registry/projects/tachyon")
  await expect(page.getByRole("heading", { name: "Tachyon", level: 2 })).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Name" })).toHaveValue("Tachyon")
  await expect(
    page.getByRole("list", { name: "Projects" }).getByRole("button", { name: /^Example App/ }),
  ).toBeVisible()
  await shot(page, "registry")
})

test("old builder URLs land where they used to", async ({ page }) => {
  await page.goto("/builder/projects")
  await expect(page).toHaveURL(/\/registry\/projects$/)
})

test("agent ops teams: a running team's members and operations, and a refused action shows its message", async ({
  page,
}) => {
  await page.goto("/agents")
  await page.getByRole("tab", { name: "Teams" }).click()
  await expect(page.getByRole("button", { name: /^Running 1/ })).toBeVisible()
  await page.getByRole("button", { name: /^Delivery/ }).click()
  const team = page
    .getByRole("dialog")
    .filter({ has: page.getByRole("heading", { name: "Delivery" }) })
  await expect(team.getByRole("cell", { name: "agt_e2e_reviewer" })).toBeVisible()
  await expect(team.getByRole("heading", { name: "Manage this run" })).toBeVisible()
  await shot(page, "agents-teams")
  await team.getByRole("combobox", { name: "Team address" }).selectOption({ label: "Whole team" })
  await team
    .getByRole("textbox", { name: "Message or assignment briefing" })
    .fill("Please pick up the release notes.")
  await team.getByRole("button", { name: "Preview provider action" }).click()
  if (!builderBackend) {
    // The stub has no team operations: the preview is refused with its message.
    await expect(
      team.getByText("Team actions reach Tether; not available in this build yet."),
    ).toBeVisible()
    return
  }
  // The backend has team operations on (builder_changes): the preview is retained as a prepared receipt, applying it
  // reaches the fake, and a request the fake refuses is retained unconfirmed with the binary's message.
  await expect(team.getByRole("status")).toHaveText("teams.address · prepared · saved revision 1")
  await team
    .getByRole("checkbox", { name: "I reviewed this exact request and its effects." })
    .check()
  await team.getByRole("button", { name: "Apply to Tether" }).click()
  await expect(team.getByRole("status")).toHaveText("teams.address · succeeded · saved revision 1")
  await expect(team.getByText(/"delivery_keys"/)).toBeVisible()
  await team.getByRole("button", { name: "Close receipt" }).click()
  await team
    .getByRole("textbox", { name: "Message or assignment briefing" })
    .fill("REFUSE this one.")
  await team.getByRole("button", { name: "Preview provider action" }).click()
  await expect(team.getByRole("status")).toHaveText("teams.address · prepared · saved revision 1")
  await team
    .getByRole("checkbox", { name: "I reviewed this exact request and its effects." })
    .check()
  await team.getByRole("button", { name: "Apply to Tether" }).click()
  await expect(team.getByRole("status")).toHaveText("teams.address · unknown · saved revision 1")
  await expect(
    team.getByText(
      "Tether returned HTTP 503. The operation is not confirmed; retain this request for reconciliation.",
    ),
  ).toBeVisible()
  await expect(team.getByRole("button", { name: "Retry same request" })).toBeVisible()
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: builder agents and registry fit", async ({ page }) => {
    for (const path of ["/builder/agents", "/registry/projects"]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        390,
      )
    }
    await shot(page, "registry-narrow")
  })
})
