import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const url = "/api/plugins/console/v1/forge/pulls/pending"
function snapshot(value: string, age = 0, state = "live") {
  return new Response(JSON.stringify({ value }), { headers: {
    "Content-Type": "application/json", "X-Tachyon-Source": "forge", "X-Tachyon-Cache": state,
    "X-Tachyon-Cache-Enabled": "true", "X-Tachyon-Updated-At": new Date(Date.now() - age).toISOString(),
    "X-Tachyon-Fresh-Seconds": "30", "X-Tachyon-Revalidate-Seconds": "3600",
  } })
}

beforeEach(() => { vi.resetModules() })
afterEach(() => { vi.unstubAllGlobals() })

describe("cache-first reads", () => {
  it("returns a warm navigation read without another network round trip", async () => {
    const fetch = vi.fn(async () => snapshot("saved"))
    vi.stubGlobal("fetch", fetch)
    const { hostFetch } = await import("./freshness")
    await hostFetch(url)
    const warm = await hostFetch(url)
    expect(await warm.json()).toEqual({ value: "saved" })
    expect(warm.headers.get("X-Tachyon-Cache")).toBe("cached")
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it("returns an older disk snapshot before revalidation finishes, then replaces it in the mounted consumer", async () => {
    let resolve!: (r: Response) => void
    const waiting = new Promise<Response>(r => { resolve = r })
    const fetch = vi.fn().mockResolvedValueOnce(snapshot("old", 40_000, "refreshing")).mockReturnValueOnce(waiting)
    vi.stubGlobal("fetch", fetch)
    const { hostFetch, dataRefreshRegistry } = await import("./freshness")
    const changed = new Promise<void>(done => dataRefreshRegistry("forge").subscribe(done))
    const old = await hostFetch(url)
    expect(await old.json()).toEqual({ value: "old" })
    expect(old.headers.get("X-Tachyon-Cache")).toBe("refreshing")
    expect(fetch.mock.calls[1][1].headers.get("Cache-Control")).toBe("no-cache")
    resolve(snapshot("new"))
    await changed
    expect(await (await hostFetch(url)).json()).toEqual({ value: "new" })
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it("coalesces cold readers and keeps one reader's cancellation from cancelling another", async () => {
    let resolve!: (r: Response) => void
    const fetch = vi.fn(() => new Promise<Response>(r => { resolve = r }))
    vi.stubGlobal("fetch", fetch)
    const { hostFetch } = await import("./freshness")
    const ctl = new AbortController()
    const first = hostFetch(url, { signal: ctl.signal })
    const other = hostFetch(url)
    ctl.abort()
    await expect(first).rejects.toHaveProperty("name", "AbortError")
    resolve(snapshot("shared"))
    expect(await (await other).json()).toEqual({ value: "shared" })
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it("removes cached identity data on access denial", async () => {
    const denied = () => new Response('{"error":"denied"}', { status: 401, headers: {
      "X-Tachyon-Source": "forge", "X-Tachyon-Cache": "error", "X-Tachyon-Cache-Error": "authentication",
    } })
    const fetch = vi.fn().mockResolvedValueOnce(snapshot("private")).mockImplementation(async () => denied())
    vi.stubGlobal("fetch", fetch)
    const { hostFetch } = await import("./freshness")
    await hostFetch(url)
    expect((await hostFetch(url, { headers: { "Cache-Control": "no-cache" } })).status).toBe(401)
    expect((await hostFetch(url)).status).toBe(401)
  })

  it("keeps the existing snapshot with an error when a background connection fails", async () => {
    let reject!: (e: Error) => void
    const waiting = new Promise<Response>((_, r) => { reject = r })
    const fetch = vi.fn().mockResolvedValueOnce(snapshot("old", 40_000, "refreshing")).mockReturnValueOnce(waiting)
    vi.stubGlobal("fetch", fetch)
    const { hostFetch, refreshPageData } = await import("./freshness")
    await hostFetch(url)
    reject(new Error("offline"))
    await refreshPageData()
    const retained = await hostFetch(url)
    expect(await retained.json()).toEqual({ value: "old" })
    expect(retained.headers.get("X-Tachyon-Cache-Error")).toBe("host_unreachable")
  })

  it("does not discard Forge reads when an unrelated preference is written", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(snapshot("saved")).mockResolvedValueOnce(new Response("{}"))
    vi.stubGlobal("fetch", fetch)
    const { hostFetch } = await import("./freshness")
    await hostFetch(url)
    await hostFetch("/api/plugins/console/v1/console/state", { method: "POST", body: "{}" })
    expect(await (await hostFetch(url)).json()).toEqual({ value: "saved" })
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it("does not restore an old snapshot when its body finishes after a successful mutation", async () => {
    let finishBody!: (body: string) => void
    let started!: () => void
    const reading = new Promise<void>(resolve => { started = resolve })
    const body = new Promise<string>(resolve => { finishBody = resolve })
    const old = snapshot("old")
    const copy = old.clone()
    vi.spyOn(copy, "text").mockImplementation(() => { started(); return body })
    vi.spyOn(old, "clone").mockReturnValueOnce(copy)
    const fetch = vi.fn().mockResolvedValueOnce(old).mockResolvedValueOnce(new Response("{}"))
      .mockResolvedValueOnce(snapshot("confirmed"))
    vi.stubGlobal("fetch", fetch)
    const { hostFetch } = await import("./freshness")
    const pending = hostFetch(url)
    await reading
    await hostFetch("/api/plugins/console/v1/forge/review", { method: "POST", body: "{}" })
    finishBody('{"value":"old"}')
    await pending
    expect(await (await hostFetch(url)).json()).toEqual({ value: "confirmed" })
    expect(fetch).toHaveBeenCalledTimes(3)
  })
})
