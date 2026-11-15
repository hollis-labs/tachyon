import { describe, expect, it } from "vitest"
import { LANDING, legacyRedirect, surfacePath } from "./surfaces"

// The shape of the manifest's surface routes, not a copy of them: the mapping is what is tested.
const ROUTES = [
  "/planner",
  "/agents",
  "/builder",
  "/registry",
  "/chat",
  "/approvals",
  "/dashboards/transcripts",
  "/repos",
  "/settings",
  "/user",
  "/identity/keys",
]

describe("surfacePath", () => {
  it.each([
    ["/planner", "/planner"],
    ["/planner/", "/planner"],
    ["/planner/themes/abc", "/planner"],
    ["/planner/streams/0b6f", "/planner"],
    ["/agents/sess-1", "/agents"],
    ["/builder/library/agent/a1", "/builder"],
    ["/builder/classic", "/builder"],
    ["/registry/projects/p1", "/registry"],
    ["/chat/thread-9", "/chat"],
    ["/approvals", "/approvals"],
    ["/dashboards/transcripts", "/dashboards/transcripts"],
    ["/repos", "/repos"],
    ["/pr/github.com/hollis-labs/tachyon/693", "/repos"],
    ["/settings/appearance", "/settings"],
    ["/user", "/user"],
  ])("%s belongs to %s", (path, area) => {
    expect(surfacePath(path, ROUTES)).toBe(area)
  })

  it("passes other paths through, so the shell can say the page was not found", () => {
    expect(surfacePath("/identity/keys", ROUTES)).toBe("/identity/keys")
    expect(surfacePath("/no-such-page", ROUTES)).toBe("/no-such-page")
    expect(surfacePath("/plannerx", ROUTES)).toBe("/plannerx")
    expect(surfacePath("/practice", ROUTES)).toBe("/practice")
  })
})

describe("legacyRedirect", () => {
  it.each([
    ["/", LANDING],
    ["/tasks", LANDING],
    ["/tasks/CW-1", LANDING],
    ["/prs/inbox", "/repos"],
  ])("%s goes to %s", (from, to) => {
    expect(legacyRedirect(from)).toBe(to)
  })

  it("leaves current paths alone", () => {
    for (const p of ["/planner", "/repos", "/taskset", "/identity/sign-out"]) {
      expect(legacyRedirect(p)).toBeNull()
    }
  })
})

describe("nested surface routes", () => {
  it("a longer surface route wins over its parent", () => {
    expect(
      surfacePath("/dashboards/transcripts/x", ["/dashboards", "/dashboards/transcripts"]),
    ).toBe("/dashboards/transcripts")
  })
})
