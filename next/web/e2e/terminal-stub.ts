/**
 * The terminal contract stub (G8a): the REST routes the chat drawer's terminal calls and a fake PTY WebSocket, in the
 * old build's shapes (internal/chatterminal). This deterministic UI lane runs alongside the real-backend lane
 * (`TACHYON_E2E_TERMINAL=backend`), which uses real handlers and a local PTY process. The contract is documented in
 * next/web/CONVERSION_NOTES.md, "Terminal". Errors are flat `{code, message}`.
 *
 * WebSocket (`ws_path` from POST sessions): PTY bytes both ways as binary frames; the client sends a text frame
 * `{"type":"resize","cols","rows"}`; the server sends text control frames `{"type":"state","state"}` and
 * `{"type":"exit","code"}`. The fake echoes every input byte back and records resizes.
 *
 * `store.refuseCreate` answers POST sessions with 403 `operator_required`; `store.refuseUpgrade` closes the socket at
 * once (a refused upgrade).
 */
import type { Page, Route } from "@playwright/test"

export const TERMINAL = "/api/plugins/console/v1/terminal"

export function terminalSeed() {
  return {
    refuseCreate: false,
    refuseUpgrade: false,
    resizes: [] as { cols: number; rows: number }[],
    closed: [] as string[],
  }
}

export async function terminalStub(page: Page) {
  const store = terminalSeed()
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  let n = 0
  await page.route(`**${TERMINAL}/**`, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const method = req.method()
    const path = url.pathname.slice(TERMINAL.length)
    if (method === "GET" && path === "/machines")
      return json(route, 200, {
        machines: [{ machine_id: "example-box", label: "example-box", reachable: true }],
      })
    if (method === "GET" && path === "/proposals") return json(route, 200, { proposals: [] })
    if (method === "POST" && path === "/sessions") {
      if (store.refuseCreate)
        return json(route, 403, {
          code: "operator_required",
          message: "Opening a terminal needs operator access.",
        })
      n += 1
      return json(route, 201, {
        id: `term-e2e-${n}`,
        ws_path: `${TERMINAL}/sessions/term-e2e-${n}/ws`,
      })
    }
    const del = path.match(/^\/sessions\/([^/]+)$/)
    if (method === "DELETE" && del) {
      store.closed.push(decodeURIComponent(del[1]))
      return json(route, 200, { ok: true })
    }
    return json(route, 404, { code: "not_found", message: "no such route" })
  })
  await page.routeWebSocket(new RegExp(`${TERMINAL}/sessions/[^/]+/ws$`), (ws) => {
    if (store.refuseUpgrade) {
      ws.close({ code: 1008, reason: "operator access required" })
      return
    }
    ws.send(JSON.stringify({ type: "state", state: "open" }))
    ws.onMessage((message) => {
      if (typeof message === "string") {
        try {
          const f = JSON.parse(message) as { type?: string; cols?: number; rows?: number }
          if (f.type === "resize" && f.cols && f.rows)
            store.resizes.push({ cols: f.cols, rows: f.rows })
        } catch {
          /* ignore */
        }
        return
      }
      ws.send(message) // echo the typed bytes back, as a PTY would
    })
  })
  return { store }
}
