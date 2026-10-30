import { describe, expect, it } from "vitest"
import { minutesLeft } from "./AccessCard"
import { outcomeLabel, relativeTime } from "./DecisionsCard"
import { initials } from "./profile"

describe("user helpers", () => {
  it("labels only approved and denied outcomes", () => {
    expect(outcomeLabel("approved")).toBe("Approved")
    expect(outcomeLabel("denied")).toBe("Denied")
    expect(outcomeLabel("expired")).toBeNull()
  })
  it("rounds time-left up to at least a minute", () => {
    expect(minutesLeft(5)).toBe("1 min left")
    expect(minutesLeft(721)).toBe("13 min left")
  })
  it("formats relative time", () => {
    const now = Date.parse("2026-10-30T12:00:00Z")
    expect(relativeTime("2026-10-30T11:58:00Z", now)).toBe("2 min ago")
    expect(relativeTime("2026-10-28T12:00:00Z", now)).toBe("2 d ago")
  })
  it("takes up to two initials", () => {
    expect(initials("")).toBe("?")
    expect(initials("Ada Lovelace")).toBe("AL")
  })
})
