import { describe, expect, it } from "vitest"
import { took } from "./data"

describe("took", () => {
  it("handles ms numbers and ISO strings, and is empty when unknown", () => {
    expect(took({ name: "x", started_at: 1000, ended_at: 1900 })).toBe("900 ms")
    expect(took({ name: "x", started_at: "2026-10-31T04:05:53.352Z" as never, ended_at: "2026-10-31T04:05:55.452Z" as never })).toBe("2.1 s")
    expect(took({ name: "x" })).toBe("")
    expect(took({ name: "x", started_at: "garbage" as never, ended_at: 5 })).toBe("")
  })
})
