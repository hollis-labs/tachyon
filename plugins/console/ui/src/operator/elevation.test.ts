import { describe, expect, it, vi } from "vitest"
import { createOperatorElevation } from "./elevation"

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  })
const locked = {
  elevated: false,
  assurance: "loopback-unverified",
  operator_token_configured: true,
}
const unlocked = { ...locked, elevated: true, assurance: "operator-token" }
const denied = () => json({ code: "assurance_insufficient" }, 403)

describe("operator elevation", () => {
  it("boots a participant, verifies elevation, and retries the exact refused action once", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(denied())
      .mockResolvedValueOnce(json({ code: "session_required" }, 401))
      .mockResolvedValueOnce(json({ connected: true }))
      .mockResolvedValueOnce(json(locked))
      .mockResolvedValueOnce(json(unlocked))
      .mockResolvedValueOnce(json(unlocked))
      .mockResolvedValueOnce(json({ sent: true }))
    const auth = createOperatorElevation(fetcher)
    const request = {
      method: "POST",
      body: JSON.stringify({ text: "hello", idempotency_key: "same-key" }),
    }
    const result = auth.fetch("/send", request)
    await vi.waitFor(() => expect(auth.snapshot().open).toBe(true))
    await auth.submit("test-key")
    expect((await result).ok).toBe(true)
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      "/send",
      expect.stringMatching(/\/status$/),
      expect.stringMatching(/\/session$/),
      expect.stringMatching(/\/status$/),
      expect.stringMatching(/\/elevate$/),
      expect.stringMatching(/\/status$/),
      "/send",
    ])
    expect(fetcher.mock.calls.at(-1)?.[1]).toBe(request)
    expect(auth.snapshot()).toEqual({
      open: false,
      submitting: false,
      error: "",
    })
  })
  it("keeps the action pending after a bad key, and cancellation never retries it", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(denied())
      .mockResolvedValueOnce(json(locked))
      .mockResolvedValueOnce(json({ code: "elevation_refused" }, 403))
    const auth = createOperatorElevation(fetcher)
    const result = auth.fetch("/send", { method: "POST" })
    await vi.waitFor(() => expect(auth.snapshot().open).toBe(true))
    await auth.submit("bad-test-key")
    expect(auth.snapshot()).toEqual({
      open: true,
      submitting: false,
      error: "That operator key was not accepted.",
    })
    auth.cancel()
    expect((await result).status).toBe(403)
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      "/send",
      expect.stringMatching(/\/status$/),
      expect.stringMatching(/\/elevate$/),
    ])
  })
  it.each([
    json({ code: "scope_denied", message: "required scope: operate" }, 403),
    json({ code: "unreachable" }, 502),
  ])("does not elevate or retry other failures", async (response) => {
    const fetcher = vi.fn().mockResolvedValue(response)
    const auth = createOperatorElevation(fetcher)
    expect(await auth.fetch("/send", { method: "POST" })).toBe(response)
    expect(auth.snapshot().open).toBe(false)
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual(["/send"])
  })
  it("does not loop when an action still refuses assurance after unlock", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(denied())
      .mockResolvedValueOnce(json(unlocked))
      .mockResolvedValueOnce(denied())
    const auth = createOperatorElevation(fetcher)
    expect((await auth.fetch("/send", { method: "POST" })).status).toBe(403)
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      "/send",
      expect.stringMatching(/\/status$/),
      "/send",
    ])
  })
  it("shares a prompt between concurrent actions and checks authority again after expiry", async () => {
    let elevated = false
    const fetcher = vi.fn(async (url: RequestInfo | URL) => {
      if (String(url).endsWith("/status"))
        return json(elevated ? unlocked : locked)
      if (String(url).endsWith("/elevate")) {
        elevated = true
        return json(unlocked)
      }
      throw new Error("unexpected request")
    })
    const auth = createOperatorElevation(fetcher)
    const first = auth.ensure()
    const second = auth.ensure()
    await vi.waitFor(() => expect(auth.snapshot().open).toBe(true))
    await auth.submit("test-key")
    expect(await Promise.all([first, second])).toEqual([true, true])
    elevated = false
    const expired = auth.ensure()
    await vi.waitFor(() => expect(auth.snapshot().open).toBe(true))
    auth.cancel()
    expect(await expired).toBe(false)
  })
  it("does not trust a successful elevate response without a verified session", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json(locked))
      .mockResolvedValueOnce(json(unlocked))
      .mockResolvedValueOnce(json(locked))
    const auth = createOperatorElevation(fetcher)
    const result = auth.ensure()
    await vi.waitFor(() => expect(auth.snapshot().open).toBe(true))
    await auth.submit("test-key")
    expect(auth.snapshot().open).toBe(true)
    expect(auth.snapshot().error).toMatch(/not be confirmed/)
    auth.cancel()
    expect(await result).toBe(false)
  })
})
