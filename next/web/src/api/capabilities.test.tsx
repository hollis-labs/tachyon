import { act, cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { CapabilitiesProvider, hasScope, OPERATOR_SCOPE, useCan, useScopes } from "./capabilities"
import { UNAUTHORIZED_EVENT } from "./request"

describe("hasScope", () => {
  it("the owner's * grants everything, including operator", () => {
    expect(hasScope(["*"], OPERATOR_SCOPE)).toBe(true)
    expect(hasScope(["*"], "tachyon.planner.write")).toBe(true)
  })

  it("an exact scope grants only itself", () => {
    expect(hasScope(["tachyon.planner.read"], "tachyon.planner.read")).toBe(true)
    expect(hasScope(["tachyon.planner.read"], "tachyon.planner.write")).toBe(false)
    expect(hasScope(["tachyon.planner.read"], OPERATOR_SCOPE)).toBe(false)
  })

  it("a prefix wildcard grants its own subtree only", () => {
    expect(hasScope(["tachyon.*"], "tachyon.chat.write")).toBe(true)
    expect(hasScope(["tachyon.planner.*"], "tachyon.planner.write")).toBe(true)
    expect(hasScope(["tachyon.planner.*"], "tachyon.chat.write")).toBe(false)
    expect(hasScope(["tachyon.*"], "tachyonx.read")).toBe(false)
  })

  it("unknown scopes grant nothing", () => {
    expect(hasScope(null, "tachyon.planner.read")).toBe(false)
    expect(hasScope([], "tachyon.planner.read")).toBe(false)
  })
})

function Probe() {
  const scopes = useScopes()
  const operator = useCan(OPERATOR_SCOPE)
  return (
    <p>{scopes === null ? "unknown" : `${scopes.join(",")}|${operator ? "operator" : "not"}`}</p>
  )
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("CapabilitiesProvider", () => {
  it("reads the scopes from the profile", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response('{"scopes":["*"]}', { status: 200 })),
    )
    render(
      <CapabilitiesProvider>
        <Probe />
      </CapabilitiesProvider>,
    )
    expect(await screen.findByText("*|operator")).toBeTruthy()
  })

  it("reads them again when the tab becomes visible; a 401 then ends the session", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('{"scopes":["tachyon.planner.read"]}', { status: 200 }))
      .mockResolvedValueOnce(
        new Response('{"code":"unauthenticated","message":"x"}', { status: 401 }),
      )
    vi.stubGlobal("fetch", fetchMock)
    const ended = vi.fn()
    window.addEventListener(UNAUTHORIZED_EVENT, ended)
    render(
      <CapabilitiesProvider>
        <Probe />
      </CapabilitiesProvider>,
    )
    expect(await screen.findByText("tachyon.planner.read|not")).toBeTruthy()
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"))
    })
    window.removeEventListener(UNAUTHORIZED_EVENT, ended)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(ended).toHaveBeenCalledOnce()
  })
})
