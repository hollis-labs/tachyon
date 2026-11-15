// The port (next/web): chat requests against the contract (flat errors, 204 deletes).
import { afterEach, describe, expect, it, vi } from "vitest"
import { chatJSON } from "./roster"

afterEach(() => vi.unstubAllGlobals())
const answer = (status: number, body: string, headers: Record<string, string> = {}) =>
  vi.fn(async () => new Response(body || null, { status, headers: { "Content-Type": "application/json", ...headers } }))

describe("chatJSON", () => {
  it("a 204 (plan delete) succeeds with no body", async () => {
    vi.stubGlobal("fetch", answer(204, ""))
    await expect(chatJSON("/threads/t/plans/p?expected_revision=1", { method: "DELETE" })).resolves.toBeUndefined()
  })

  it("an error shows the flat body's message", async () => {
    vi.stubGlobal("fetch", answer(409, '{"code":"conflict","message":"The plan changed; reload it."}'))
    await expect(chatJSON("/threads/t/plans", { method: "POST", body: "{}" })).rejects.toThrow("The plan changed; reload it.")
  })

  it("an empty 200 is still refused as an invalid response", async () => {
    vi.stubGlobal("fetch", answer(200, ""))
    await expect(chatJSON("/threads")).rejects.toThrow("invalid response")
  })

  it("sends X-Requested-With (through the host fetch)", async () => {
    const f = answer(200, '{"threads":[]}')
    vi.stubGlobal("fetch", f)
    await chatJSON("/threads", { method: "POST", body: "{}" })
    const init = (f.mock.calls[0] as unknown as [string, RequestInit])[1]
    expect(new Headers(init.headers).get("X-Requested-With")).toBe("tachyon-ui")
  })
})
