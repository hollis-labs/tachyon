import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { MemoryRouter } from "react-router-dom"
import { ApprovalProvider, HOLD_MS } from "./context"
import { ApprovalsPage } from "./page"
import type { ApprovalClient, ApprovalItem, ActionRun } from "./client"
import { ActionRunPanel, RUN_STATE_COPY, terminalURL } from "./terminal"

// xterm needs a real canvas and layout; the panel's own behaviour is what is
// under test, so the terminal and its fit addon are replaced.
vi.mock("../operator/elevation", () => ({ operatorFetch: (...args: Parameters<typeof fetch>) => fetch(...args) }))

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
  vi.useRealTimers()
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

// Exercise the actual provider, undo hold, queue selection and run panel together.
// Only the browser terminal transport/layout is replaced above.

describe("queue to interactive run continuation", () => {
  it.each([false, true])("retains the action through hold and queue refresh (another pending item: %s)", async (another) => {
    vi.stubGlobal("WebSocket", FakeSocket)
    vi.stubGlobal("ResizeObserver", FakeObserver)
    let run: ActionRun = { ...base, params: { plan_id: "plan-1" }, requester: { label: "@chief", task: "CW-20261115-0125" } }
    const request: ApprovalItem = {
      item_id: base.item_id, revision: 1, state: "presented", enqueued_at: base.created_at, updated_at: base.created_at,
      action_run: run,
      request_snapshot: { kind: "approval", class: "medium", title: "Approve catalog plan", source: { agent_id: "service:credential", agent_label: "@chief", application_id: "tachyon-actions" } },
    }
    const other = { ...request, item_id: "other", action_run: undefined, request_snapshot: { ...request.request_snapshot, title: "Other request" }, enqueued_at: "2026-10-24T00:00:00Z" }
    let approved = false
    const client = {
      inbox: vi.fn(async () => ({ pending: approved ? (another ? [other] : []) : [request, ...(another ? [other] : [])], history: [] })),
      present: vi.fn(async () => approved ? { ...request, state: "resolved", action_run: run } : request),
      audit: vi.fn(async () => ({ item_id: request.item_id, events: [] })),
      decide: vi.fn(async () => { approved = true; run = { ...run, state: "waiting_input" }; return { item_id: request.item_id, state: "resolved" } }),
    } as unknown as ApprovalClient
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(run), { status: 200 })))
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    const approve = await screen.findByRole("button", { name: "Approve and run" })
    await waitFor(() => expect((approve as HTMLButtonElement).disabled).toBe(false))
    vi.useFakeTimers()
    fireEvent.click(approve)
    expect(screen.getByRole("region", { name: "Action run" })).toBeTruthy()
    expect(client.decide).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(HOLD_MS + 1600) })
    vi.useRealTimers()
    await waitFor(() => expect(screen.getByRole("group", { name: /Terminal for/ })).toBeTruthy(), { timeout: 3000 })
    expect(client.decide).toHaveBeenCalledTimes(1)
    expect(screen.getByText(/Run run_1/)).toBeTruthy()
    expect(screen.getAllByText("CW-20261115-0125").length).toBeGreaterThan(0)
    expect(screen.getByText(/Catalog plan plan-1/)).toBeTruthy()
    // Reconnection attaches the same authorized run; it never sends another decision.
    act(() => FakeSocket.last.onclose?.())
    fireEvent.click(screen.getByRole("button", { name: "Reconnect terminal" }))
    expect(FakeSocket.last.url).toContain("run_1/terminal")
    const connected = FakeSocket.last
    fireEvent.click(screen.getByRole("button", { name: "Refresh approvals" }))
    fireEvent.click(screen.getByRole("button", { name: "Refresh approvals" }))
    await act(async () => {})
    expect(FakeSocket.last).toBe(connected)
    expect(client.decide).toHaveBeenCalledTimes(1)
    run = { ...run, state: "failed", terminal: true, exit_code: 2, ended_at: "2026-10-23T00:01:00Z" }
    await act(async () => FakeSocket.last.onmessage?.({ data: JSON.stringify({ type: "exit", state: "failed", code: 2 }) }))
    await screen.findByText(/Failed · exit 2/)
    expect(client.decide).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole("group", { name: /Terminal for/ })).toBeNull()
  })
})

describe("action history reload", () => {
  it.each(["waiting_input", "denied", "expired"] as const)("reopens a linked run outside the inbox page in state %s", async (state) => {
    vi.stubGlobal("WebSocket", FakeSocket)
    vi.stubGlobal("ResizeObserver", FakeObserver)
    const run = { ...base, state, terminal: state !== "waiting_input" }
    const request: ApprovalItem = {
      item_id: base.item_id, revision: 2, state: "resolved", enqueued_at: base.created_at, updated_at: base.created_at,
      action_run: run,
      request_snapshot: { kind: "approval", title: "Historical catalog action", source: { agent_id: "chief", application_id: "tachyon-actions" } },
    }
    const client = { inbox: vi.fn(async () => ({ pending: [], history: [] })), present: vi.fn(async () => request), audit: vi.fn(async () => ({ item_id: request.item_id, events: [] })), decide: vi.fn() } as unknown as ApprovalClient
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(run))))
    render(<MemoryRouter initialEntries={["/approvals?item=item_1"]}><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    await screen.findByRole("region", { name: "Action run" })
    if (state === "waiting_input") await screen.findByRole("group", { name: /Terminal for/ })
    else expect(screen.queryByRole("group", { name: /Terminal for/ })).toBeNull()
    expect(client.decide).not.toHaveBeenCalled()
  })
})
