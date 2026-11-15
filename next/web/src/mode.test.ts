import { describe, expect, it } from "vitest"
import { DEFAULT_MODE, readMode, resolveMode } from "./mode"

describe("mode", () => {
  it("defaults to system when nothing or junk is stored", () => {
    expect(readMode({ getItem: () => null })).toBe(DEFAULT_MODE)
    expect(readMode({ getItem: () => "p4-white" })).toBe(DEFAULT_MODE)
    expect(
      readMode({
        getItem: () => {
          throw new Error("blocked")
        },
      }),
    ).toBe(DEFAULT_MODE)
  })

  it("reads a stored choice", () => {
    expect(readMode({ getItem: () => "light" })).toBe("light")
  })

  it("resolves system against the OS preference", () => {
    expect(resolveMode("system", true)).toBe("dark")
    expect(resolveMode("system", false)).toBe("light")
    expect(resolveMode("dark", false)).toBe("dark")
  })
})
