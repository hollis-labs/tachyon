import { describe, expect, it } from "vitest"
import { LANDING_ROUTES, landingRoute } from "./Landing"

describe("landingRoute", () => {
  it("maps each landing page value to its section", () => {
    expect(landingRoute("chat", "/planner")).toBe("/chat")
    expect(landingRoute("transcripts", "/planner")).toBe("/dashboards/transcripts")
    expect(Object.keys(LANDING_ROUTES)).toEqual([
      "planner",
      "chat",
      "agents",
      "approvals",
      "transcripts",
      "repos",
      "builder",
    ])
  })
  it("falls back for unknown or missing values", () => {
    expect(landingRoute("nowhere", "/planner")).toBe("/planner")
    expect(landingRoute(undefined, "/planner")).toBe("/planner")
  })
})

describe("optionLabel", () => {
  it("names landing pages as the nav does and reads other enum values in sentence case", async () => {
    const { optionLabel } = await import("./general")
    expect(optionLabel("landing_page", "repos")).toBe("Pull requests")
    expect(optionLabel("landing_page", "planner")).toBe("Planner")
    expect(optionLabel("scratchpad_placement", "drawer")).toBe("Drawer")
  })
})
