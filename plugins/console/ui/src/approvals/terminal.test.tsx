import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import type { ActionRun } from "./client"
import { ActionRunPanel, RUN_STATE_COPY, terminalURL } from "./terminal"

// xterm needs a real canvas and layout; the panel's own behaviour is what is
// under test, so the terminal and its fit addon are replaced.
const terminals: { data?: (d: string) => void; written: Uint8Array[] }[] = []
vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 90
    rows = 25
    state = { written: [] as Uint8Array[], data: undefined as undefined | ((d: string) => void) }
    constructor() {
      terminals.push(this.state)
    }
    loadAddon() {}
    open() {}
    write(b: Uint8Array) {
      this.state.written.push(b)
    }
    onData(cb: (d: string) => void) {
      this.state.data = cb
      return { dispose() {} }
    }
    onResize() {
      return { dispose() {} }
    }
    selectAll() {}
    getSelection() {
      return ""
    }
    clearSelection() {}
    dispose() {}
  },
}))
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }))
vi.mock("@xterm/xterm/css/xterm.css", () => ({}))

class FakeSocket {
  static last: FakeSocket
  static OPEN = 1
  readyState = 1
  binaryType = ""
  sent: unknown[] = []
  onopen?: () => void
  onmessage?: (e: { data: unknown }) => void
  onclose?: () => void
  constructor(public url: string) {
    FakeSocket.last = this
    queueMicrotask(() => this.onopen?.())
  }
  send(d: unknown) {
    this.sent.push(d)
  }
  close() {}
}

const base: ActionRun = {
  run_id: "run_1", item_id: "item_1", machine: "hub", action: "tether.catalog.approve",
  argv: ["tether", "catalog", "approve", "plan-1"], command: "tether catalog approve plan-1",
  op_digest: "sha256:" + "a".repeat(64), class: "medium", state: "pending_approval", terminal: false,
  interactive: true, created_at: "2026-10-23T00:00:00Z",
}

class FakeObserver {
  observe() {}
  disconnect() {}
}

afterEach(() => {
  cleanup()
  terminals.length = 0
  vi.unstubAllGlobals()
})

describe("action run panel", () => {
  it("shows machine, exact command and digest before approval, and no terminal", () => {
    vi.stubGlobal("WebSocket", FakeSocket)
    render(<ActionRunPanel initial={base} approved={false} fetcher={vi.fn()} />)
    expect(screen.getByText("▸ hub")).toBeTruthy()
    expect(screen.getByText("tether catalog approve plan-1")).toBeTruthy()
    expect(screen.getByText(base.op_digest)).toBeTruthy()
    expect(screen.getByText(RUN_STATE_COPY.pending_approval)).toBeTruthy()
    expect(terminals).toHaveLength(0)
  })

  it("opens the terminal when an approved interactive run waits, sends size and raw input frames", async () => {
    vi.stubGlobal("WebSocket", FakeSocket)
    vi.stubGlobal("ResizeObserver", FakeObserver)
    const waiting = { ...base, state: "waiting_input" as const }
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(waiting), { status: 200 }))
    render(<ActionRunPanel initial={waiting} approved fetcher={fetcher} pollMs={60000} />)
    await waitFor(() => expect(terminals).toHaveLength(1))
    await waitFor(() => expect(FakeSocket.last.sent).toHaveLength(1))
    expect(FakeSocket.last.url).toBe(terminalURL("run_1", window.location))
    expect(JSON.parse(String(FakeSocket.last.sent[0]))).toEqual({ type: "resize", cols: 90, rows: 25 })
    act(() => terminals[0].data?.("abc\r"))
    expect(new TextDecoder().decode(FakeSocket.last.sent[1] as Uint8Array)).toBe("abc\r")
    act(() => FakeSocket.last.onmessage?.({ data: new TextEncoder().encode("Digest prefix: ").buffer }))
    expect(terminals[0].written).toHaveLength(1)
  })

  it("shows exit status and recorded output once the run ends", () => {
    const done = { ...base, state: "failed" as const, terminal: true, exit_code: 3, output: "boom", started_at: "2026-10-23T00:00:00Z", ended_at: "2026-10-23T00:00:05Z" }
    render(<ActionRunPanel initial={done} approved fetcher={vi.fn()} />)
    expect(screen.getByText(/Failed · exit 3 · 5s/)).toBeTruthy()
    expect(screen.getByText("boom")).toBeTruthy()
  })

  it("shows the fixed note, not output, for a finished interactive run", () => {
    const done = { ...base, state: "succeeded" as const, terminal: true, exit_code: 0, note: "Terminal output of interactive actions is not recorded. See the status and exit code." }
    render(<ActionRunPanel initial={done} approved fetcher={vi.fn()} />)
    expect(screen.getByText(/not recorded/)).toBeTruthy()
    expect(screen.queryByText(/Recorded output/)).toBeNull()
  })

  it("states a digest refusal plainly", () => {
    const refused = { ...base, state: "refused" as const, terminal: true, error: "op_digest mismatch" }
    render(<ActionRunPanel initial={refused} approved fetcher={vi.fn()} />)
    expect(screen.getByText(/Refused:/)).toBeTruthy()
    expect(screen.getByText("op_digest mismatch")).toBeTruthy()
  })

  it("builds a same-origin websocket URL", () => {
    expect(terminalURL("run_1", { protocol: "https:", host: "tachyon.example" })).toBe("wss://tachyon.example/api/plugins/console/v1/action-runs/run_1/terminal")
    expect(terminalURL("run_1", { protocol: "http:", host: "127.0.0.1:8080" })).toMatch(/^ws:\/\/127\.0\.0\.1:8080\//)
  })
})
