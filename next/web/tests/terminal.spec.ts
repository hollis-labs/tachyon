import type { Page } from "@playwright/test"
import { THREAD } from "../e2e/chat-stub"
import { backend } from "../e2e/env"
import { TERMINAL } from "../e2e/terminal-stub"
import { expect, terminalBackend, test } from "./fixtures"
import { owner, shot } from "./helpers"

// Stub mode checks the written UI contract; backend mode uses real handlers and a real PTY,
// with only the external ssh executable replaced by a local echo process.
test.use({ storageState: owner })

const ROW = "chat://tether/launch/L_e2e"
const openTerminal = async (page: Page) => {
  await page.goto(`/chat/${encodeURIComponent(ROW)}`)
  await page.getByRole("button", { name: "Terminal", exact: true }).click()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeVisible()
}
const status = (page: Page) => page.getByRole("status").filter({ hasText: /^ssh · saved key/ })

test("connect, type and see the echo, resize, then disconnect", async ({ page, terminal }) => {
  const sent: (string | Buffer)[] = []
  const received: (string | Buffer)[] = []
  page.on("websocket", (ws) => {
    if (!ws.url().includes(`${TERMINAL}/sessions/`)) return
    ws.on("framesent", (f) => sent.push(f.payload))
    ws.on("framereceived", (f) => received.push(f.payload))
  })
  const resizes = () =>
    terminal.store?.resizes ??
    sent.filter((f) => typeof f === "string" && JSON.parse(f).type === "resize")
  await openTerminal(page)
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(status(page)).toHaveText("ssh · saved key · open")
  const term = page.getByRole("group", { name: "Terminal session" })
  await term.locator("textarea").focus() // xterm's input element
  await page.keyboard.type("echo port-ok")
  await page.keyboard.press("Enter")
  await expect(term.locator(".xterm-rows")).toContainText("echo port-ok")
  await expect.poll(() => resizes().length).toBeGreaterThan(0)
  if (terminalBackend) {
    expect(JSON.parse(sent[0] as string)).toMatchObject({ type: "resize" })
    const first = JSON.parse(sent[0] as string)
    expect(first.cols).toBeGreaterThan(0)
    expect(first.cols).toBeLessThanOrEqual(500)
    expect(first.rows).toBeGreaterThan(0)
    expect(first.rows).toBeLessThanOrEqual(500)
    expect(sent.some((f) => Buffer.isBuffer(f))).toBe(true)
    expect(received.some((f) => Buffer.isBuffer(f))).toBe(true)
    expect(received.some((f) => typeof f === "string" && JSON.parse(f).state === "open")).toBe(true)
    await expect(term.locator(".xterm-rows")).toContainText("fake-ssh-ready")
  }
  const before = resizes().length
  await page.setViewportSize({ width: 1000, height: 700 })
  await expect.poll(() => resizes().length).toBeGreaterThan(before)
  // Below 1100px the Agent Panel overlays the conversation (carried layout); back to the default width to reach the bar.
  await page.setViewportSize({ width: 1280, height: 720 })
  await shot(page, "terminal")
  const closed = page.waitForResponse(
    (r) => r.request().method() === "DELETE" && r.url().includes(`${TERMINAL}/sessions/`),
  )
  await page.getByRole("button", { name: "Disconnect", exact: true }).click()
  expect((await closed).status()).toBe(200)
  await expect(term).toHaveCount(0)
  if (terminal.store) await expect.poll(() => terminal.store?.closed.length).toBe(1)
})

test("a refused session shows the backend's message", async ({ page, terminal }) => {
  test.skip(!terminal.store, "the stub's refusal")
  if (!terminal.store) return
  terminal.store.refuseCreate = true
  await openTerminal(page)
  const sent = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === `${TERMINAL}/sessions`,
  )
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await sent
  await expect(
    page.getByRole("alert").filter({ hasText: "Opening a terminal needs operator access." }),
  ).toBeVisible()
  await shot(page, "terminal-refused")
})

test("a refused upgrade closes the session and says so", async ({ page, terminal }) => {
  test.skip(!terminal.store, "the stub's refusal")
  if (!terminal.store) return
  terminal.store.refuseUpgrade = true
  await openTerminal(page)
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(status(page)).toHaveText("ssh · saved key · disconnected")
})

test.describe("narrow", () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test("390px: the terminal opens and fits", async ({ page }) => {
    await openTerminal(page)
    await page.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page.getByRole("group", { name: "Terminal session" })).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await shot(page, "terminal-narrow")
  })
})

test("backend: operator and bearer guards apply to reads and writes", async ({
  page,
  playwright,
}) => {
  test.skip(!terminalBackend, "real terminal guards")
  const headers = { "X-Requested-With": "tachyon-ui", Origin: backend }
  const outsider = await playwright.request.newContext({
    baseURL: backend,
    storageState: { cookies: [], origins: [] },
    extraHTTPHeaders: headers,
  })
  try {
    expect((await outsider.get(`${TERMINAL}/machines`)).status()).toBe(401)
    for (const operator of [false, true]) {
      const minted = await page.request.post("/api/token", {
        headers,
        data: { scopes: ["tachyon.terminal.control", ...(operator ? ["tachyon.operator"] : [])] },
      })
      expect(minted.status()).toBe(200)
      const { token } = await minted.json()
      for (const method of ["GET", "POST"]) {
        const response = await outsider.fetch(
          `${TERMINAL}/${method === "GET" ? "machines" : "sessions"}`,
          {
            method,
            headers: { ...headers, Authorization: `Bearer ${token}` },
            ...(method === "POST" ? { data: { machine_id: "example-box" } } : {}),
          },
        )
        expect(response.status()).toBe(403)
        expect((await response.json()).code).toBe(
          operator ? "requester_cannot_approve" : "operator_required",
        )
      }
    }
  } finally {
    await outsider.dispose()
  }
})

test("backend: origin refusal, binary replay, exit and ended-session conflict", async ({
  page,
}) => {
  test.skip(!terminalBackend, "real terminal WebSocket contract")
  const headers = { "X-Requested-With": "tachyon-ui", Origin: backend }
  const opened = await page.request.post(`${TERMINAL}/sessions`, {
    headers,
    data: { machine_id: "example-box", thread_ref: THREAD },
  })
  expect(opened.status()).toBe(201)
  const { id, ws_path: path } = await opened.json()
  try {
    const upgrade = {
      Upgrade: "websocket",
      Connection: "Upgrade",
      "Sec-WebSocket-Version": "13",
      "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
    }
    const origins: Record<string, string>[] = [
      { Origin: "http://elsewhere.test" },
      {},
      { Origin: backend, "Sec-Fetch-Site": "cross-site" },
    ]
    for (const originHeaders of origins) {
      const refused = await page.request.get(path, { headers: { ...upgrade, ...originHeaders } })
      expect(refused.status()).toBe(403)
      expect((await refused.json()).code).toBe("origin_denied")
    }
    await page.goto(`/chat/${encodeURIComponent(ROW)}`)
    const proof = await page.evaluate(async (wsPath) => {
      const connect = (exit: boolean) =>
        new Promise<{ output: string; state: boolean; exitCode?: number }>((resolve, reject) => {
          const ws = new WebSocket(`${location.origin.replace(/^http/, "ws")}${wsPath}`)
          ws.binaryType = "arraybuffer"
          let output = ""
          let state = false
          const timer = setTimeout(() => {
            ws.close()
            reject(new Error("terminal frame timeout"))
          }, 10_000)
          const done = (exitCode?: number) => {
            clearTimeout(timer)
            ws.close()
            resolve({ output, state, exitCode })
          }
          ws.onerror = () => {
            clearTimeout(timer)
            reject(new Error("terminal upgrade refused"))
          }
          ws.onopen = () => ws.send(JSON.stringify({ type: "resize", cols: 90, rows: 20 }))
          ws.onmessage = (event) => {
            if (typeof event.data === "string") {
              const frame = JSON.parse(event.data)
              if (frame.type === "exit") {
                done(frame.code)
                return
              }
              if (frame.type === "state") {
                state = frame.state === "open"
                ws.send(new TextEncoder().encode(exit ? "exit-terminal\n" : "replay-proof\n"))
              }
            } else {
              output += new TextDecoder().decode(event.data)
            }
            if (!exit && state && output.includes("replay-proof")) done()
          }
        })
      const first = await connect(false)
      const second = await connect(true)
      return { first, second }
    }, path)
    expect(proof.first.state).toBe(true)
    expect(proof.first.output).toContain("fake-ssh-ready")
    expect(proof.second.state).toBe(true)
    expect(proof.second.output).toContain("replay-proof")
    expect(proof.second.exitCode).toBe(7)
    await expect
      .poll(async () =>
        (await page.request.get(path, { headers: { ...upgrade, Origin: backend } })).status(),
      )
      .toBe(409)
  } finally {
    expect((await page.request.delete(`${TERMINAL}/sessions/${id}`, { headers })).status()).toBe(
      200,
    )
  }
})
