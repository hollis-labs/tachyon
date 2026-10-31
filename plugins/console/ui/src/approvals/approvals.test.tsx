import {
  cleanup,
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  ApprovalError,
  handoffItems,
  createApprovalClient,
  type ApprovalItem,
  type ApprovalClient,
} from "./client"
import { noteServerNow, serverNow } from "./clock"
import { MAX_BATCH, batchEligibility, inlineDecisionBlocker, orderPending } from "./batch"
import { ApprovalDialog } from "./dialog"
import { ApprovalProvider } from "./context"
import { ApprovalsPage } from "./page"

afterEach(cleanup)
// Detail panes read the audit trail on open; clients that do not care about it return none.
const noAudit = { audit: vi.fn().mockResolvedValue({ item_id: "", events: [] }) }
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
      ...noAudit,
      present: vi.fn().mockResolvedValue(item),
      decide,
      evidence: vi.fn(),
    }) as unknown as ApprovalClient
  it("approves low and medium risk in one click, preserves notes, disables repeat submission and shows the authoritative result", async () => {
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
    expect(
      (screen.getByRole("button", { name: "Approving…" }) as HTMLButtonElement)
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
  it("lists requests in a queue, shows the selected detail beside it and filters without deciding", async () => {
    const client = {
      inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }),
      ...noAudit,
      present: vi.fn().mockResolvedValue(item),
      decide: vi.fn(),
      evidence: vi.fn(),
      get: vi.fn(),
    } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    const row = await screen.findByRole("listitem", { name: /Create a session/ })
    expect(row.getAttribute("aria-current")).toBe("true")
    await waitFor(() => expect(client.present).toHaveBeenCalledWith(item.item_id))
    fireEvent.change(screen.getByRole("searchbox", { name: "Search approvals" }), { target: { value: "unmatched" } })
    await screen.findByText("No matching requests.")
    expect(screen.queryByRole("listitem", { name: /Create a session/ })).toBeNull()
    expect(client.decide).not.toHaveBeenCalled()
  })
})

describe("owned inbox tiers and pages", () => {
  it("lets a high-risk request be approved only after typing the phrase, and hands off when the backend says it is not approvable", async () => {
    const high: ApprovalItem = { ...item, request_snapshot: { ...item.request_snapshot, class: "high" } }
    const decide = vi.fn().mockResolvedValue({ item_id: high.item_id, state: "resolved" })
    const client = { ...noAudit, present: vi.fn().mockResolvedValue(high), get: vi.fn().mockResolvedValue(high), decide } as unknown as ApprovalClient
    const { unmount } = render(<ApprovalDialog id={high.item_id} initial={high} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    const approve = screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement
    expect(approve.disabled).toBe(true)
    expect(screen.getByText("This cannot be undone.")).toBeTruthy()
    fireEvent.change(screen.getByLabelText("Confirmation phrase"), { target: { value: "approve" } })
    await waitFor(() => expect(approve.disabled).toBe(false))
    unmount()
    const blocked: ApprovalItem = { ...high, request_snapshot: { ...high.request_snapshot, approvable: false } }
    render(<ApprovalDialog id={blocked.item_id} initial={blocked} client={{ ...noAudit, present: vi.fn().mockResolvedValue(blocked), decide } as unknown as ApprovalClient} onClose={vi.fn()} onChanged={vi.fn()} />)
    await screen.findByText(/cannot be approved here/)
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull()
  })
  it("uses the server's confirm phrase, sends it as confirmation, offers the alternative and needs a reason to deny", async () => {
    const destructive: ApprovalItem = {
      ...item,
      request_snapshot: { ...item.request_snapshot, class: "medium" },
      confirm: { phrase: "DELETE 3 ITEMS" },
      destructive: { irreversible: true, summary: "Deletes three environments." },
      affected_items: [{ ref: "e:1", label: "scratch-1", note: "idle 40 days" }, { ref: "e:2", label: "scratch-2" }, { ref: "e:3", label: "scratch-3" }],
      affected_total: 7,
      alternatives: [{ id: "archive", label: "Archive instead" }],
      reason_required: true,
    }
    const decide = vi.fn().mockResolvedValue({ item_id: destructive.item_id, state: "resolved" })
    const client = { ...noAudit, present: vi.fn().mockResolvedValue(destructive), get: vi.fn().mockResolvedValue(destructive), decide } as unknown as ApprovalClient
    render(<ApprovalDialog id={destructive.item_id} initial={destructive} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    await screen.findByText("Deletes three environments.")
    expect(screen.getByText("scratch-1 (idle 40 days)")).toBeTruthy()
    expect(screen.getByText("+ 4 more")).toBeTruthy()
    const approve = screen.getByRole("button", { name: /^Approve/ }) as HTMLButtonElement
    fireEvent.change(screen.getByLabelText("Confirmation phrase"), { target: { value: "delete 3 items" } })
    await waitFor(() => expect(approve.disabled).toBe(true)) // case-sensitive
    fireEvent.change(screen.getByLabelText("Confirmation phrase"), { target: { value: "DELETE 3 ITEMS" } })
    await waitFor(() => expect(approve.disabled).toBe(false))
    expect((screen.getByRole("button", { name: /^Deny/ }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText(/A reason is needed to deny/)).toBeTruthy()
    fireEvent.click(approve)
    await waitFor(() => expect(decide).toHaveBeenCalledTimes(1))
    expect(decide.mock.calls[0][1]).toMatchObject({ decision: "approved", confirmation: "DELETE 3 ITEMS" })
  })
  it("approves an alternative with its id and no confirmation phrase", async () => {
    const withAlt: ApprovalItem = { ...item, confirm: { phrase: "DELETE 3 ITEMS" }, alternatives: [{ id: "archive", label: "Archive instead" }] }
    const decide = vi.fn().mockResolvedValue({ item_id: withAlt.item_id, state: "resolved" })
    const client = { ...noAudit, present: vi.fn().mockResolvedValue(withAlt), get: vi.fn().mockResolvedValue(withAlt), decide } as unknown as ApprovalClient
    render(<ApprovalDialog id={withAlt.item_id} initial={withAlt} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    fireEvent.click(await screen.findByRole("button", { name: "Archive instead" }))
    await waitFor(() => expect(decide).toHaveBeenCalled())
    const sent = decide.mock.calls[0][1]
    expect(sent).toMatchObject({ decision: "approved", alternative_id: "archive" })
    expect(sent.confirmation).toBeUndefined()
  })
  it("switches to the provider handoff state on provider_handoff_required, without the raw error, and remembers it", async () => {
    const high: ApprovalItem = { ...item, request_snapshot: { ...item.request_snapshot, class: "high", handoff_url: "https://cerberus.example/approve/one" } }
    const decide = vi.fn().mockRejectedValue(new ApprovalError(403, "provider_handoff_required", "provider handoff required"))
    const client = { ...noAudit, present: vi.fn().mockResolvedValue(high), get: vi.fn().mockResolvedValue(high), decide } as unknown as ApprovalClient
    const { unmount } = render(<ApprovalDialog id={high.item_id} initial={high} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    fireEvent.change(await screen.findByLabelText("Confirmation phrase"), { target: { value: "approve" } })
    await waitFor(() => expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
    await screen.findByText(/cannot be approved here/)
    expect(screen.queryByText(/provider handoff required/)).toBeNull()
    expect(screen.queryByRole("alert")).toBeNull()
    expect(screen.getByRole("link", { name: /Open in/ })).toBeTruthy()
    unmount()
    render(<ApprovalDialog id={high.item_id} initial={high} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    await screen.findByText(/cannot be approved here/)
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull()
    handoffItems.delete(high.item_id)
  })
  it("loads bounded next pages and can return to the newest page", async () => {
    const second = { ...item, item_id: "older", request_snapshot: { ...item.request_snapshot, title: "Older request" } }
    const inbox = vi.fn().mockImplementation(async (_fresh, cursors) => ({ pending: cursors?.pending_before ? [second] : [item], history: [], next_pending: cursors?.pending_before ? undefined : "req-1", synced_at: new Date().toISOString(), revision: "1" }))
    render(<MemoryRouter><ApprovalProvider client={{ ...noAudit, inbox } as unknown as ApprovalClient}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    fireEvent.click(await screen.findByRole("button", { name: "Next page" }))
    await screen.findByRole("listitem", { name: /Older request/ })
    expect(inbox).toHaveBeenLastCalledWith(true, { pending_before: "req-1" })
    fireEvent.click(screen.getByRole("button", { name: "First page" }))
    await screen.findByRole("listitem", { name: /Create a session/ })
  })
})

describe("held decisions", () => {
  it("holds an approval in a toast so it can be undone, and sends nothing when undone", async () => {
    const decide = vi.fn().mockResolvedValue({ item_id: item.item_id, state: "resolved" })
    const client = { ...noAudit, inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }), present: vi.fn().mockResolvedValue(item), get: vi.fn(), decide, evidence: vi.fn() } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    await waitFor(() => expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
    expect(screen.getByRole("list", { name: "Pending decisions" }).textContent).toMatch(/Approving in \d+s/)
    expect(decide).not.toHaveBeenCalled()
    // The held row is dimmed and selection has nowhere else to go (last item).
    fireEvent.click(screen.getByRole("button", { name: /Undo/ }))
    expect(screen.queryByRole("list", { name: "Pending decisions" })).toBeNull()
    // Undo re-selects the item with its note intact.
    await waitFor(() => expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false))
    expect(decide).not.toHaveBeenCalled()
  })
})

describe("history", () => {
  it("sorts newest first and keeps one group per day", async () => {
    const day = (n: number) => new Date(Date.now() - n * 86400000).toISOString()
    const ev = (id: string, at: string, extra = {}) => ({ event_id: id, item_id: id, kind: "expired", status: "expired", at, actor: { ref: "", label: "System" }, mini: { title: id }, ...extra })
    const history = vi.fn().mockResolvedValue({ events: [ev("old1", day(3)), ev("new1", day(0)), ev("mid", day(1), { reason: "deadline" }), ev("new2", day(0))] })
    const client = { ...noAudit, inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }), present: vi.fn().mockResolvedValue(item), history } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    fireEvent.click(await screen.findByRole("tab", { name: /History/ }))
    const list = await screen.findByRole("list", { name: "Decision history" })
    const text = list.textContent ?? ""
    expect(text.match(/Today/g)).toHaveLength(1)
    expect(text.indexOf("Today")).toBeLessThan(text.indexOf("Yesterday"))
    expect(text.indexOf("Yesterday")).toBeLessThan(text.indexOf("Expiredold1"))
    expect(text).toContain("Deadline passed")
  })
  it("shows decisions under day pills with who and why, and opens an item", async () => {
    const now = new Date().toISOString()
    const history = vi.fn().mockResolvedValue({ facets: { requesters: [{ id: "agent-one", label: "agent-one", count: 3 }, { id: "agent-two", label: "agent-two", count: 1 }], statuses: [] }, events: [
      { event_id: "e1", item_id: "req-1", kind: "resolved", status: "approved", at: now, actor: { ref: "s", label: "You" }, mini: { title: "Create a session" }, note: "Looks right" },
      { event_id: "e2", item_id: "req-2", kind: "expired", status: "expired", at: now, actor: { ref: "", label: "System" }, mini: { title: "Rotate token" } },
    ] })
    const client = { ...noAudit, inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }), present: vi.fn().mockResolvedValue(item), history } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    fireEvent.click(await screen.findByRole("tab", { name: /History/ }))
    const list = await screen.findByRole("list", { name: "Decision history" })
    expect(list.textContent).toContain("Today")
    expect(list.textContent).toContain("ApprovedCreate a session")
    expect(list.textContent).toContain("Looks right")
    expect(list.textContent).toContain("ExpiredRotate token")
    expect(history).toHaveBeenCalledWith({ q: "", status: "", requester: "", cursor: undefined })
    // Filters go to the server: a decision menu and a requester field.
    fireEvent.click(screen.getByRole("button", { name: "Filter by requester" }))
    await act(async () => {})
    // Requesters come from the server's facets when present.
    expect(screen.getByRole("menuitem", { name: "agent-two" })).toBeTruthy()
    fireEvent.click(screen.getByRole("menuitem", { name: "agent-one" }))
    await waitFor(() => expect(history).toHaveBeenLastCalledWith({ q: "", status: "", requester: "agent-one", cursor: undefined }))
    fireEvent.click(screen.getByRole("tab", { name: /Denied/ }))
    await waitFor(() => expect(history).toHaveBeenLastCalledWith({ q: "", status: "denied", requester: "agent-one", cursor: undefined }))
    expect(screen.getByRole("tab", { name: /Denied/ }).getAttribute("aria-selected")).toBe("true")
  })
})

describe("approval deep link", () => {
  it("selects the request for /approvals?item=<id> and clears the parameter", async () => {
    const present = vi.fn().mockResolvedValue(item)
    const client = { ...noAudit, inbox: vi.fn().mockResolvedValue({ pending: [item], history: [] }), present } as unknown as ApprovalClient
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
    ({ ...noAudit, present: vi.fn().mockResolvedValue(current), decide, evidence: vi.fn(), storeSecret }) as unknown as ApprovalClient
  const storeSecret = vi.fn()
  const open = (current: ApprovalItem, decide: ReturnType<typeof vi.fn>) =>
    render(<ApprovalDialog id={current.item_id} initial={current} client={clientFor(current, decide)} onClose={vi.fn()} onChanged={vi.fn()} />)

  it("shows a card's effects and approves with the card kind", async () => {
    const current = card("card.approval", { changes: 2 }, { effects: [{ target: "torque:task/CW-1", op: "status", value: "done" }] })
    const decide = vi.fn().mockResolvedValue({ item_id: current.item_id, state: "resolved" })
    open(current, decide)
    await waitFor(() => expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false))
    expect(screen.getByText("op status · target torque:task/CW-1 · value done")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
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
    expect(screen.queryByRole("textbox", { name: "Token ref" })).toBeNull()
    storeSecret.mockResolvedValue({ secret_ref: "secret://tachyon/abc", expires_at: 1, fingerprint: "sha256:1a2b3c4d" })
    fireEvent.click(screen.getByRole("button", { name: "Enter securely…" }))
    const dlg = await screen.findByRole("dialog")
    const field = within(dlg).getByLabelText("Secret value") as HTMLInputElement
    expect(field.type).toBe("password")
    fireEvent.change(field, { target: { value: "hunter2" } })
    fireEvent.click(within(dlg).getByRole("button", { name: "Store secret" }))
    await screen.findByText("1a2b3c4d")
    expect(storeSecret).toHaveBeenCalledWith(expect.objectContaining({ itemId: "item_card", field: "token", value: "hunter2" }))
    expect(screen.queryByText("hunter2")).toBeNull()
    expect(screen.getByText("1a2b3c4d")).toBeTruthy()
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(submit)
    expect(decide.mock.calls[0][1]).toEqual({
      kind: "card.form",
      decision: "submitted",
      answers: [
        { questionId: "name", value: "x" },
        { questionId: "count", value: 3 },
        { questionId: "token", value: { secret_ref: "secret://tachyon/abc" } },
      ],
    })
  })
  it("keeps the entered secret and retries with the same key when operator access is needed, and drops it on cancel", async () => {
    const current = card("card.form", { fields: [{ id: "token", type: "secret", label: "Token", required: true }] })
    open(current, vi.fn())
    storeSecret.mockReset()
    storeSecret
      .mockRejectedValueOnce(new ApprovalError(403, "assurance_insufficient", "step up", { action: "secret_submit" }))
      .mockResolvedValueOnce({ secret_ref: "secret://tachyon/z", expires_at: 1, fingerprint: "sha256:ffffffff" })
    fireEvent.click(await screen.findByRole("button", { name: "Enter securely…" }))
    const dlg = await screen.findByRole("dialog")
    fireEvent.change(within(dlg).getByLabelText("Secret value"), { target: { value: "s3cret" } })
    fireEvent.click(within(dlg).getByRole("button", { name: "Store secret" }))
    await within(dlg).findByText(/Operator access is needed/)
    expect((within(dlg).getByLabelText("Secret value") as HTMLInputElement).value).toBe("s3cret")
    fireEvent.click(within(dlg).getByRole("button", { name: "Try again" }))
    await screen.findByText("ffffffff")
    expect(storeSecret.mock.calls[1][0].idempotencyKey).toBe(storeSecret.mock.calls[0][0].idempotencyKey)
    fireEvent.click(screen.getByRole("button", { name: "Replace" }))
    const again = await screen.findByRole("dialog")
    expect((within(again).getByLabelText("Secret value") as HTMLInputElement).value).toBe("")
  })
  it("answers a question card under the id answer and leaves info cards unanswerable", async () => {
    const q = card("card.question", { question: "Which branch?", options: ["main", "dev"] })
    const decide = vi.fn().mockResolvedValue({ item_id: q.item_id, state: "resolved" })
    open(q, decide)
    const submit = await screen.findByRole("button", { name: "Send answer" })
    fireEvent.change(screen.getByLabelText("Which branch?"), { target: { value: "dev" } })
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(submit)
    expect(decide.mock.calls[0][1]).toEqual({ kind: "card.question", decision: "submitted", answers: [{ questionId: "answer", value: "dev" }] })
    cleanup()
    open(card("card.info", { fact: 1 }), vi.fn())
    await screen.findByText(/nothing to answer/)
    expect(screen.queryByRole("button", { name: "Send answer" })).toBeNull()
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
    const client = { ...noAudit, inbox: vi.fn().mockResolvedValue({ pending: [item, form], history: [], synced_at: "2026-10-23T00:00:00Z", revision: "1" }), get: vi.fn(), present: vi.fn(), decide: vi.fn(), evidence: vi.fn() } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    expect(await screen.findByText(/· form$/)).toBeTruthy()
    expect(screen.getByText(/· approval$/)).toBeTruthy()
    fireEvent.change(screen.getByLabelText("Search approvals"), { target: { value: "form" } })
    await waitFor(() => expect(screen.queryByRole("listitem", { name: /Create a session/ })).toBeNull())
    expect(screen.getByRole("listitem", { name: /Fill me in/ })).toBeTruthy()
  })
})

describe("approval defer", () => {
  it("snoozes with the reviewed revision and brings the item back with DELETE", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json({ item_id: "req-1", deferred_until: "2026-10-17T21:00:00Z", revision: 4 }))
      .mockResolvedValueOnce(json({ item_id: "req-1", deferred_until: null, revision: 4 }))
    const client = createApprovalClient(fetcher)
    await client.defer(item, 3600)
    expect(fetcher.mock.calls[0][0]).toMatch(/\/items\/req-1\/defer$/)
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ expected_revision: 4, duration_s: 3600 })
    await client.undefer("req-1")
    expect(fetcher.mock.calls[1][1].method).toBe("DELETE")
  })
  it("offers Decide later and shows the snoozed state with Bring back now", async () => {
    const defer = vi.fn().mockResolvedValue({ item_id: "req-1", deferred_until: "2099-01-01T00:00:00Z", revision: 5 })
    const undefer = vi.fn().mockResolvedValue({ item_id: "req-1", deferred_until: null, revision: 5 })
    const changed = vi.fn()
    const client = { ...noAudit, present: vi.fn().mockResolvedValue(item), decide: vi.fn(), defer, undefer } as unknown as ApprovalClient
    render(<ApprovalDialog id={item.item_id} initial={item} client={client} onClose={vi.fn()} onChanged={changed} />)
    await waitFor(() => expect((screen.getByRole("button", { name: "Deny" }) as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(screen.getByRole("button", { name: "Decide later" }))
    await act(async () => {})
    fireEvent.click(screen.getByRole("menuitem", { name: "4 hours" }))
    await screen.findByText(/Snoozed until/)
    expect(defer).toHaveBeenCalledWith(expect.objectContaining({ item_id: "req-1" }), 4 * 3600)
    expect(changed).toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "Bring back now" }))
    await screen.findByRole("button", { name: "Decide later" })
    expect(undefer).toHaveBeenCalledWith("req-1")
  })
})

describe("defer near expiry and decision keys", () => {
  const soon = (ms: number): ApprovalItem => ({
    ...item,
    request_snapshot: { ...item.request_snapshot, expires_at: new Date(Date.now() + ms).toISOString() },
  })
  const clientFor = (it: ApprovalItem, decide = vi.fn()) =>
    ({ ...noAudit, present: vi.fn().mockResolvedValue(it), decide, defer: vi.fn(), undefer: vi.fn() }) as unknown as ApprovalClient
  it("offers only presets that end before the request expires", async () => {
    const it = soon(5 * 3600 * 1000)
    render(<ApprovalDialog id={it.item_id} initial={it} client={clientFor(it)} onClose={vi.fn()} onChanged={vi.fn()} />)
    await waitFor(() => expect((screen.getByRole("button", { name: "Deny" }) as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(screen.getByRole("button", { name: "Decide later" }))
    await act(async () => {})
    expect(screen.getByRole("menuitem", { name: "4 hours" })).toBeTruthy()
    expect(screen.queryByRole("menuitem", { name: "Tomorrow" })).toBeNull()
  })
  it("says so when nothing fits before expiry", async () => {
    const it = soon(10 * 60 * 1000)
    render(<ApprovalDialog id={it.item_id} initial={it} client={clientFor(it)} onClose={vi.fn()} onChanged={vi.fn()} />)
    await screen.findByText(/cannot be snoozed/)
    expect(screen.queryByRole("button", { name: "Decide later" })).toBeNull()
  })
  it("D on the focused decision bar denies; A on a high-risk request does nothing until the phrase is typed", async () => {
    const decide = vi.fn().mockResolvedValue({ item_id: "req-1", state: "resolved" })
    const high: ApprovalItem = { ...item, request_snapshot: { ...item.request_snapshot, class: "high" } }
    render(<ApprovalDialog id={high.item_id} initial={high} client={clientFor(high, decide)} onClose={vi.fn()} onChanged={vi.fn()} />)
    await waitFor(() => expect((screen.getByRole("button", { name: "Deny" }) as HTMLButtonElement).disabled).toBe(false))
    fireEvent.keyDown(screen.getByRole("button", { name: "Approve" }), { key: "a" })
    expect(decide).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole("button", { name: "Deny" }), { key: "d" })
    await waitFor(() => expect(decide).toHaveBeenCalled())
  })
})


// Base UI checkboxes toggle through their hidden input.
const tick = (name: string) =>
  act(() => {
    screen.getByRole("checkbox", { name }).parentElement?.querySelector<HTMLInputElement>("input[type=checkbox]")?.click()
  })

describe("batch approve", () => {
  const low = (id: string, batchable: boolean): ApprovalItem => ({
    ...item,
    item_id: id,
    batchable,
    request_snapshot: { ...item.request_snapshot, title: `Request ${id}`, class: "low" },
  })
  it("sends one batch with each reviewed revision and a batch_id", async () => {
    const fetcher = vi.fn().mockResolvedValue(json({ batch_id: "b1", results: [], counts: {} }))
    await createApprovalClient(fetcher).batchResolve([item], "approved", "b1")
    expect(fetcher.mock.calls[0][0]).toMatch(/\/batch\/resolve$/)
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({
      batch_id: "b1",
      decision: "approved",
      items: [{ item_id: "req-1", expected_revision: 4, presented_projection_revision: 3 }],
    })
  })
  it("offers checkboxes only on batchable rows, confirms, and reports what stayed", async () => {
    const a = low("a", true)
    const b = low("b", true)
    const c = low("c", false)
    const batchResolve = vi.fn().mockResolvedValue({ batch_id: "x", results: [], counts: { resolved: 1, stale: 1 } })
    const client = {
      inbox: vi.fn().mockResolvedValue({ pending: [a, b, c], history: [] }),
      ...noAudit,
      present: vi.fn().mockResolvedValue(a),
      access: vi.fn().mockResolvedValue({ state: "elevated", can_elevate: true, required_for: { batch: true } }),
      batchResolve,
    } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    await screen.findByRole("listitem", { name: /Request a/ })
    expect((screen.getByRole("checkbox", { name: /Request c cannot be batched/ }) as HTMLElement).getAttribute("data-disabled")).not.toBeNull()
    tick("Select Request a")
    tick("Select Request b")
    fireEvent.click(await screen.findByRole("button", { name: "Approve 2" }))
    expect(batchResolve).not.toHaveBeenCalled()
    expect(screen.getByText(/Approve 2 requests\? This cannot be undone/)).toBeTruthy()
    fireEvent.keyDown(window, { key: "Enter", ctrlKey: true })
    await screen.findByText("1 approved, 1 left in the queue to review one by one.")
    expect(batchResolve).toHaveBeenCalledWith([a, b], "approved")
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }))
    expect(screen.queryByText(/left in the queue/)).toBeNull()
  })
  it("orders the queue: snoozed last, then expiry, risk, age", () => {
    const mk = (id: string, o: { exp?: string; cls?: "low" | "high"; at?: string; def?: string }) =>
      ({ item_id: id, enqueued_at: o.at ?? "2026-10-30T00:00:00Z", deferred_until: o.def ?? null, request_snapshot: { class: o.cls ?? "medium", expires_at: o.exp } }) as unknown as ApprovalItem
    const now = Date.parse("2026-10-30T12:00:00Z")
    const out = orderPending([
      mk("snoozed", { def: "2026-10-30T18:00:00Z", exp: "2026-10-30T13:00:00Z" }),
      mk("noexp", {}),
      mk("late", { exp: "2026-10-30T15:00:00Z" }),
      mk("soon-low", { exp: "2026-10-30T14:00:00Z", cls: "low" }),
      mk("soon-high", { exp: "2026-10-30T14:00:00Z", cls: "high" }),
    ], now)
    expect(out.map((i) => i.item_id)).toEqual(["soon-high", "soon-low", "late", "noexp", "snoozed"])
  })
  it("turns the decision off and says why when operator access is missing", async () => {
    const a = low("a", true)
    const client = {
      inbox: vi.fn().mockResolvedValue({ pending: [a], history: [] }),
      ...noAudit,
      present: vi.fn().mockResolvedValue(a),
      access: vi.fn().mockResolvedValue({ state: "none", can_elevate: true, required_for: { batch: true } }),
    } as unknown as ApprovalClient
    render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    await screen.findByRole("listitem", { name: /Request a/ })
    tick("Select Request a")
    await screen.findByText(/Operator access needed/)
    expect((screen.getByRole("button", { name: "Approve 1" }) as HTMLButtonElement).disabled).toBe(true)
  })
})

describe("batch eligibility", () => {
  it("explains a refusal from the server's blockers and caps a batch at 25", () => {
    expect(batchEligibility({ batchable: true })).toEqual({ ok: true, reasons: [] })
    expect(batchEligibility({ batchable: false, batch_blockers: ["risk_not_low", "needs_review"] }).reasons).toEqual(["Not low risk: review it on its own.", "Needs review."])
    expect(batchEligibility({ batchable: false }).reasons).toEqual(["Review it on its own."])
    expect(MAX_BATCH).toBe(25)
  })
})

describe("inline decisions", () => {
  it("keeps typed confirmations and required reasons on the Approvals page", () => {
    expect(inlineDecisionBlocker({ confirm: { phrase: "DELETE 3 ITEMS" } }, { decision: "approved" })).toMatch(/typed confirmation/)
    expect(inlineDecisionBlocker({ confirm: { phrase: "X" } }, { decision: "denied" })).toBeUndefined()
    expect(inlineDecisionBlocker({ reason_required: true }, { decision: "denied" })).toMatch(/reason/)
    expect(inlineDecisionBlocker({ reason_required: true }, { decision: "denied", note: "no" })).toBeUndefined()
    expect(inlineDecisionBlocker({}, { decision: "approved" })).toBeUndefined()
  })
})

describe("server clock", () => {
  it("shifts countdowns by the server's offset and ignores a bad value", () => {
    const local = 1_000_000
    noteServerNow(local + 60_000, local)
    expect(serverNow() - Date.now()).toBeCloseTo(60_000, -2)
    noteServerNow("nope")
    expect(serverNow() - Date.now()).toBeCloseTo(60_000, -2)
    noteServerNow(Date.now())
    expect(Math.abs(serverNow() - Date.now())).toBeLessThan(50)
  })
})

describe("approval stream", () => {
  class FakeES {
    static last: FakeES | undefined
    readyState = 0
    onopen?: () => void
    onerror?: () => void
    handlers: Record<string, () => void> = {}
    closed = false
    constructor(public url: string) { FakeES.last = this }
    addEventListener(n: string, h: () => void) { this.handlers[n] = h }
    close() { this.closed = true }
  }
  afterEach(() => { vi.unstubAllGlobals(); FakeES.last = undefined })
  it("refreshes on a stream event, says it polls only while the stream is down, and closes on unmount", async () => {
    vi.stubGlobal("EventSource", FakeES)
    const inbox = vi.fn().mockResolvedValue({ pending: [], history: [] })
    const client = { inbox, ...noAudit } as unknown as ApprovalClient
    const { unmount } = render(<MemoryRouter><ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider></MemoryRouter>)
    await screen.findByText("Updates every 10 s")
    // The stream opens only after the first inbox load, once the session cookie exists.
    await waitFor(() => expect(FakeES.last).toBeDefined())
    expect(inbox).toHaveBeenCalled()
    expect(FakeES.last!.url).toMatch(/\/approval-inbox\/events$/)
    await act(async () => FakeES.last!.onopen?.())
    await waitFor(() => expect(screen.queryByText("Updates every 10 s")).toBeNull())
    const before = inbox.mock.calls.length
    await act(async () => FakeES.last!.handlers["item.created"]?.())
    await waitFor(() => expect(inbox.mock.calls.length).toBeGreaterThan(before))
    await act(async () => FakeES.last!.onerror?.())
    await screen.findByText("Updates every 10 s")
    unmount()
    expect(FakeES.last!.closed).toBe(true)
  })
})

describe("audit trail", () => {
  it("lists a request's audit events with a count", async () => {
    const decided: ApprovalItem = { ...item, state: "resolved", terminal_outcome: { item_id: "req-1", state: "resolved", resolution: { response: { kind: "approval", decision: "approved" }, resolved_at: "2026-10-30T10:00:00Z", participant: { principal_ref: "operator", authority: "queue", assurance: "participant" } } } as never }
    const audit = vi.fn().mockResolvedValue({ item_id: "req-1", events: [
      { event_id: "a1", ts: "2026-10-30T09:00:00Z", kind: "presented", actor: { ref: "s", label: "You" } },
      { event_id: "a2", ts: "2026-10-30T10:00:00Z", kind: "resolved", decision: "approved", note: "Looks right", actor: { ref: "s", label: "You" } },
    ] })
    const client = { present: vi.fn().mockResolvedValue(decided), decide: vi.fn(), audit } as unknown as ApprovalClient
    render(<ApprovalDialog id="req-1" initial={decided} client={client} onClose={vi.fn()} onChanged={vi.fn()} />)
    await screen.findByText("Decision audit (2)")
    const list = await screen.findByRole("list", { name: "Audit trail" })
    expect(list.textContent).toContain("Presented by you")
    expect(list.textContent).toContain("Approved by you")
    expect(list.textContent).toContain("Looks right")
    expect(audit).toHaveBeenCalledWith("req-1")
  })
})
