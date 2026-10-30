import { describe, expect, it } from "vitest"
import { DEFAULT_SECTION, resolveSection } from "./sections"

describe("resolveSection", () => {
  it("accepts reachable sections", () => {
    expect(resolveSection("appearance")).toBe("appearance")
  })
  it("falls back to Account for unknown or soon sections", () => {
    expect(resolveSection(undefined)).toBe(DEFAULT_SECTION)
    expect(resolveSection("nope")).toBe(DEFAULT_SECTION)
    expect(resolveSection("agents")).toBe(DEFAULT_SECTION)
  })
})
