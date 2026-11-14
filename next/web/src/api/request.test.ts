import { afterEach, describe, expect, it, vi } from "vitest"
import { ApiError, GENERIC_MESSAGE, parseErrorBody, request, UNAUTHORIZED_EVENT } from "./request"

function answer(status: number, body: string) {
  return vi.fn(async () => new Response(body || null, { status }))
}

afterEach(() => vi.unstubAllGlobals())

describe("request", () => {
  it("sends X-Requested-With and the same-origin cookie on every request, JSON on writes", async () => {
    const fetchMock = answer(200, '{"ok":true}')
    vi.stubGlobal("fetch", fetchMock)
    await request("/api/x", { method: "POST", body: { a: 1 } })
    const [path, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(path).toBe("/api/x")
    expect(init.method).toBe("POST")
    expect(init.credentials).toBe("same-origin")
    expect(init.body).toBe('{"a":1}')
    const headers = init.headers as Record<string, string>
    expect(headers["X-Requested-With"]).toBe("XMLHttpRequest")
    expect(headers["Content-Type"]).toBe("application/json")
  })

  it("reads also carry X-Requested-With and no Content-Type", async () => {
    const fetchMock = answer(200, "[]")
    vi.stubGlobal("fetch", fetchMock)
    await request("/api/x")
    const headers = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
      .headers as Record<string, string>
    expect(headers["X-Requested-With"]).toBe("XMLHttpRequest")
    expect(headers["Content-Type"]).toBeUndefined()
  })

  it("passes keepalive through (the replacement for sendBeacon)", async () => {
    const fetchMock = answer(204, "")
    vi.stubGlobal("fetch", fetchMock)
    await expect(request("/api/x", { method: "POST", keepalive: true })).resolves.toBeUndefined()
    expect((fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].keepalive).toBe(true)
  })

  it("throws the flat error body as ApiError with code, message, details and status", async () => {
    vi.stubGlobal(
      "fetch",
      answer(
        409,
        '{"code":"conflict","message":"Changed by someone else.","details":{"current":{"rev":4}}}',
      ),
    )
    const err = (await request("/api/x").catch((e) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(409)
    expect(err.code).toBe("conflict")
    expect(err.message).toBe("Changed by someone else.")
    expect(err.details).toEqual({ current: { rev: 4 } })
  })

  it("keeps the 403 codes apart: scope_required names the scope, operator_required does not", async () => {
    vi.stubGlobal(
      "fetch",
      answer(
        403,
        '{"code":"scope_required","message":"m","details":{"scope":"tachyon.chat.write"}}',
      ),
    )
    const scope = (await request("/api/x").catch((e) => e)) as ApiError
    expect(scope.code).toBe("scope_required")
    expect(scope.details?.scope).toBe("tachyon.chat.write")
    vi.stubGlobal("fetch", answer(403, '{"code":"operator_required","message":"m"}'))
    expect(((await request("/api/x").catch((e) => e)) as ApiError).code).toBe("operator_required")
  })

  it("a non-JSON error body still gives an ApiError with a generic message", async () => {
    vi.stubGlobal("fetch", answer(502, "<html>bad gateway</html>"))
    const err = (await request("/api/x").catch((e) => e)) as ApiError
    expect(err.status).toBe(502)
    expect(err.code).toBe("")
    expect(err.message).toBe(GENERIC_MESSAGE)
  })

  it("a 401 tells the identity gate the session ended", async () => {
    vi.stubGlobal("fetch", answer(401, '{"code":"unauthenticated","message":"Sign in."}'))
    const seen = vi.fn()
    window.addEventListener(UNAUTHORIZED_EVENT, seen)
    await request("/api/x").catch(() => {})
    window.removeEventListener(UNAUTHORIZED_EVENT, seen)
    expect(seen).toHaveBeenCalledOnce()
  })

  it("a 403 does not end the session", async () => {
    vi.stubGlobal("fetch", answer(403, '{"code":"operator_required","message":"m"}'))
    const seen = vi.fn()
    window.addEventListener(UNAUTHORIZED_EVENT, seen)
    await request("/api/x").catch(() => {})
    window.removeEventListener(UNAUTHORIZED_EVENT, seen)
    expect(seen).not.toHaveBeenCalled()
  })
})

describe("parseErrorBody", () => {
  it("reads only the flat shape and ignores the old nested one", () => {
    expect(parseErrorBody('{"code":"a","message":"b"}')).toEqual({ code: "a", message: "b" })
    expect(parseErrorBody('{"error":{"code":"a","message":"b"}}')).toEqual({})
    expect(parseErrorBody('{"code":3,"message":null,"details":[1]}')).toEqual({})
    expect(parseErrorBody("[]")).toEqual({})
    expect(parseErrorBody("")).toEqual({})
  })
})
