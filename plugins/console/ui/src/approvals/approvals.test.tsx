import {
  cleanup,
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  createApprovalClient,
  type ApprovalItem,
  type ApprovalClient,
} from "./client"
import { ApprovalDialog } from "./dialog"
import { ApprovalProvider } from "./context"
import { ApprovalsPage } from "./page"

afterEach(cleanup)
const item: ApprovalItem = {
  item_id: "req-1",
  state: "presented",
  revision: 4,
  presented_projection_revision: 3,
  enqueued_at: "2026-10-17T20:00:00Z",
  updated_at: "2026-10-17T20:00:00Z",
  request_snapshot: {
    kind: "approval",
    title: "Create a session",
    summary: "Review session creation",
    request: "Create **one** session.",
    source: { agent_id: "agent-one", application_id: "tether" },
    impact: {
      approve: "The agent may proceed.",
      deny: "The agent remains blocked.",
    },
  },
}
const json = (value: unknown, status = 200, headers = {}) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  })

describe("approval decision contract", () => {
  it("establishes a participant only for reads and carries the reviewed revision and explicit decision", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json({ code: "session_required" }, 401))
      .mockResolvedValueOnce(json({ connected: true }))
      .mockResolvedValueOnce(json({ pending: [item], history: [] }))
      .mockResolvedValueOnce(json(item))
      .mockResolvedValueOnce(json({ item_id: item.item_id, state: "resolved" }))
    const client = createApprovalClient(fetcher)
    expect((await client.inbox()).pending[0].item_id).toBe("req-1")
    await client.decide(item, {
      kind: "approval",
      decision: "approved",
      note: "One session only",
    })
    expect(fetcher.mock.calls[1][0]).toMatch(/\/session$/)
    expect(fetcher.mock.calls[3][1].headers["Cache-Control"]).toBe("no-cache")
    expect(JSON.parse(fetcher.mock.calls[4][1].body)).toEqual({
      expected_revision: 4,
      presented_projection_revision: 3,
      response: {
        kind: "approval",
        decision: "approved",
        note: "One session only",
      },
    })
  })
  it.each([
    { ...item, revision: 5 },
    { ...item, state: "resolved" },
    {
      ...item,
      request_snapshot: {
        ...item.request_snapshot,
        expires_at: "2000-01-01T00:00:00Z",
      },
    },
  ])("does not decide a changed, ended or expired request", async (current) => {
    const fetcher = vi.fn().mockResolvedValue(json(current))
    await expect(
      createApprovalClient(fetcher).decide(item, {
        kind: "approval",
        decision: "approved",
      }),
    ).rejects.toThrow(/changed|ended/)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it("refuses stale fallback and never replays a denied or ambiguous decision", async () => {
    const stale = vi
      .fn()
      .mockResolvedValue(json(item, 200, { "X-Tachyon-Cache": "stale" }))
    await expect(
      createApprovalClient(stale).present(item.item_id),
    ).rejects.toThrow(/verify/)
    expect(stale).toHaveBeenCalledTimes(1)
    const denied = vi
      .fn()
      .mockResolvedValueOnce(json(item))
      .mockResolvedValueOnce(
        json({ code: "scope_denied", message: "Approval scope denied" }, 403),
      )
    await expect(
      createApprovalClient(denied).decide(item, {
        kind: "approval",
        decision: "approved",
      }),
    ).rejects.toThrow("Approval scope denied")
    expect(denied).toHaveBeenCalledTimes(2)
  })
})

describe("approval review dialog", () => {
  const mockClient = (decide = vi.fn()) =>
    ({
      present: vi.fn().mockResolvedValue(item),
      decide,
      evidence: vi.fn(),
    }) as unknown as ApprovalClient
  it("requires explicit confirmation, preserves notes, disables repeat submission and shows the authoritative result", async () => {
    let settle!: (value: unknown) => void
    const decide = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          settle = resolve
        }),
    )
    const changed = vi.fn()
    render(
      <ApprovalDialog
        id={item.item_id}
        initial={item}
        client={mockClient(decide)}
        onClose={vi.fn()}
        onChanged={changed}
      />,
    )
    await waitFor(() =>
      expect(
        (screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    )
    expect(screen.getByText("one").tagName).toBe("STRONG")
    fireEvent.change(screen.getByLabelText("Decision note"), {
      target: { value: "Only this request" },
    })
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
    expect(decide).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Confirm decision" }))
    expect(
      (screen.getByRole("button", { name: "Submitting…" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true)
    expect(decide).toHaveBeenCalledTimes(1)
    expect(decide.mock.calls[0][1]).toEqual({
      kind: "approval",
      decision: "approved",
      note: "Only this request",
    })
    await act(async () =>
      settle({
        item_id: item.item_id,
        state: "resolved",
        resolution: {
          response: { kind: "approval", decision: "approved" },
          resolved_at: "2026-10-17T21:00:00Z",
          participant: {
            principal_ref: "operator",
            authority: "queue",
            assurance: "participant",
          },
        },
      }),
    )
    expect(screen.getByText("approved")).toBeTruthy()
    expect(changed).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull()
  })
  it("keeps a failed decision unresolved and requires a fresh review", async () => {
    const decide = vi.fn().mockRejectedValue(new Error("Request changed"))
    render(
      <ApprovalDialog
        id={item.item_id}
        initial={item}
        client={mockClient(decide)}
        onClose={vi.fn()}
        onChanged={vi.fn()}
      />,
    )
    await waitFor(() =>
      expect(
        (screen.getByRole("button", { name: "Deny" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    )
    fireEvent.click(screen.getByRole("button", { name: "Deny" }))
    fireEvent.click(screen.getByRole("button", { name: "Confirm decision" }))
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Request changed",
    )
    expect(
      (screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true)
  })
})


describe("approval list interaction", () => {
  it("filters the shared rows and opens a request from the keyboard without deciding it", async () => {
    const client = {
      inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }),
      present: vi.fn().mockResolvedValue(item),
      decide: vi.fn(),
      evidence: vi.fn(),
      get: vi.fn(),
    } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    const row = await screen.findByRole("button", { name: "Create a session" })
    row.focus()
    fireEvent.keyDown(row, { key: "Enter" })
    await screen.findByRole("dialog")
    expect(client.present).toHaveBeenCalledWith(item.item_id)
    fireEvent.click(screen.getAllByRole("button", { name: "Close" })[0])
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    fireEvent.change(screen.getByRole("searchbox", { name: "Search approvals" }), { target: { value: "unmatched" } })
    await screen.findByText("No matching requests.")
    expect(screen.queryByRole("button", { name: "Create a session" })).toBeNull()
    expect(client.decide).not.toHaveBeenCalled()
  })
})

describe("owned inbox tiers and pages", () => {
  it("hands high-tier requests to the provider without local decision controls", async () => {
    const high: ApprovalItem = { ...item, request_snapshot: { ...item.request_snapshot, class: "high", handoff_url: "https://cerberus.example/approve/one", source: { application_id: "cerberus", agent_id: "agent-one" } } }
    const client = { present: vi.fn().mockResolvedValue(high), decide: vi.fn() } as unknown as ApprovalClient
    render(<ApprovalDialog id={high.item_id} initial={high} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    expect((await screen.findByRole("link", { name: "Open in Cerberus" })).getAttribute("href")).toBe("https://cerberus.example/approve/one")
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Deny" })).toBeNull()
    expect(client.decide).not.toHaveBeenCalled()
  })
  it("loads bounded next pages and can return to the newest page", async () => {
    const second = { ...item, item_id: "older", request_snapshot: { ...item.request_snapshot, title: "Older request" } }
    const inbox = vi.fn().mockImplementation(async (_fresh, cursors) => ({ pending: cursors?.pending_before ? [second] : [item], history: [], next_pending: cursors?.pending_before ? undefined : "req-1", synced_at: new Date().toISOString(), revision: "1" }))
    render(<MemoryRouter><ApprovalProvider client={{ inbox } as unknown as ApprovalClient}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    fireEvent.click(await screen.findByRole("button", { name: "Next page" }))
    await screen.findByText("Older request")
    expect(inbox).toHaveBeenLastCalledWith(true, { pending_before: "req-1" })
    fireEvent.click(screen.getByRole("button", { name: "First page" }))
    await screen.findByText("Create a session")
  })
})

describe("approval deep link", () => {
  it("opens the review dialog for /approvals?item=<id> and clears the parameter", async () => {
    const present = vi.fn().mockResolvedValue(item)
    const client = { inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }), present } as unknown as ApprovalClient
    render(
      <MemoryRouter initialEntries={["/approvals?item=req-1"]}>
        <ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider>
      </MemoryRouter>,
    )
    await waitFor(() => expect(present).toHaveBeenCalledWith("req-1"))
  })
})

describe("card review dialog", () => {
  const card = (kind: `card.${string}`, data: Record<string, unknown>, extra = {}): ApprovalItem => ({
    ...item,
    item_id: "item_card",
    request_snapshot: {
      kind,
      title: "Planner asks",
      source: { agent_id: "planner", application_id: "service:abc" },
      class: "low",
      data,
      ...extra,
    },
  })
  const clientFor = (current: ApprovalItem, decide = vi.fn().mockResolvedValue({ item_id: current.item_id, state: "resolved" })) =>
    ({ present: vi.fn().mockResolvedValue(current), decide, evidence: vi.fn() }) as unknown as ApprovalClient
  const open = (current: ApprovalItem, decide: ReturnType<typeof vi.fn>) =>
    render(<ApprovalDialog id={current.item_id} initial={current} client={clientFor(current, decide)} onClose={vi.fn()} onChanged={vi.fn()} />)

  it("shows a card's effects and approves with the card kind", async () => {
    const current = card("card.approval", { changes: 2 }, { effects: [{ target: "torque:task/CW-1", op: "status", value: "done" }] })
    const decide = vi.fn().mockResolvedValue({ item_id: current.item_id, state: "resolved" })
    open(current, decide)
    await waitFor(() => expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false))
    expect(screen.getByText("op status · target torque:task/CW-1 · value done")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
    fireEvent.click(screen.getByRole("button", { name: "Confirm decision" }))
    expect(decide.mock.calls[0][1]).toEqual({ kind: "card.approval", decision: "approved" })
  })
  it("always shows the bound operation, and labels a requester summary as unverified", async () => {
    const current = card("card.approval", {}, {
      effects: [
        { target: "torque:task/CW-20261023-0001", op: "status", value: "done", summary: "No changes" },
        { target: "torque:task/CW-20261023-0002", op: "status" },
      ],
    })
    open(current, vi.fn())
    await screen.findByText("If approved this will")
    const rows = screen.getAllByRole("listitem")
    expect(rows[0].textContent).toContain("op status · target torque:task/CW-20261023-0001 · value done")
    expect(rows[0].textContent).toContain("Requester's description (unverified, not what runs): No changes")
    expect(rows[1].textContent).toContain("value (none)")
    expect(rows[1].textContent).not.toContain("Requester's description")
    // The summary never stands in for the operation.
    expect(screen.queryByText("No changes")).toBeNull()
  })
  it("submits a form's typed answers, with secrets only as references", async () => {
    const current = card("card.form", {
      fields: [
        { id: "name", type: "text", label: "Name", required: true },
        { id: "count", type: "number", label: "Count" },
        { id: "token", type: "secret", label: "Token ref" },
      ],
    })
    const decide = vi.fn().mockResolvedValue({ item_id: current.item_id, state: "resolved" })
    open(current, decide)
    const submit = await screen.findByRole("button", { name: "Submit" })
    expect((submit as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "x" } })
    fireEvent.change(screen.getByLabelText("Count"), { target: { value: "3" } })
    fireEvent.change(screen.getByLabelText("Token ref"), { target: { value: "vault/key" } })
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(submit)
    expect(decide.mock.calls[0][1]).toEqual({
      kind: "card.form",
      decision: "submitted",
      answers: [
        { questionId: "name", value: "x" },
        { questionId: "count", value: 3 },
        { questionId: "token", value: { secret_ref: "vault/key" } },
      ],
    })
  })
  it("answers a question card under the id answer and leaves info cards unanswerable", async () => {
    const q = card("card.question", { question: "Which branch?", options: ["main", "dev"] })
    const decide = vi.fn().mockResolvedValue({ item_id: q.item_id, state: "resolved" })
    open(q, decide)
    const submit = await screen.findByRole("button", { name: "Submit" })
    fireEvent.change(screen.getByLabelText("Which branch?"), { target: { value: "dev" } })
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(submit)
    expect(decide.mock.calls[0][1]).toEqual({ kind: "card.question", decision: "submitted", answers: [{ questionId: "answer", value: "dev" }] })
    cleanup()
    open(card("card.info", { fact: 1 }), vi.fn())
    await screen.findByText(/nothing to answer/)
    expect(screen.queryByRole("button", { name: "Submit" })).toBeNull()
  })
  it("shows what happened after an approved card's effects ran", async () => {
    const current = {
      ...card("card.approval", { n: 1 }, { effects: [{ target: "torque:task/CW-1", op: "status", value: "done" }] }),
      state: "resolved",
      card_receipt: { state: "failed", effects: [{ index: 0, target: "torque:task/CW-1", op: "status", status: "failed", error: "Torque refused: invalid transition" }] },
    } as ApprovalItem
    open(current, vi.fn())
    expect(await screen.findByText("Result · failed")).toBeTruthy()
    expect(screen.getByText(/Torque refused: invalid transition/)).toBeTruthy()
  })
})

describe("approvals list", () => {
  it("labels each row with its kind and searches by it", async () => {
    const form = { ...item, item_id: "item_form", request_snapshot: { ...item.request_snapshot, kind: "card.form" as const, title: "Fill me in" } }
    const client = { inbox: vi.fn().mockResolvedValue({ pending: [item, form], history: [], synced_at: "2026-10-23T00:00:00Z", revision: "1" }), get: vi.fn(), present: vi.fn(), decide: vi.fn(), evidence: vi.fn() } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    expect(await screen.findByText(/^Form ·/)).toBeTruthy()
    expect(screen.getByText(/^Approval ·/)).toBeTruthy()
    fireEvent.change(screen.getByLabelText("Search approvals"), { target: { value: "form" } })
    await waitFor(() => expect(screen.queryByText("Create a session")).toBeNull())
    expect(screen.getByText("Fill me in")).toBeTruthy()
  })
})
