import {
  cleanup,
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
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
    render(<ApprovalProvider client={client}><ApprovalsPage /></ApprovalProvider>)
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
    render(<ApprovalProvider client={{ inbox } as unknown as ApprovalClient}><ApprovalsPage /></ApprovalProvider>)
    fireEvent.click(await screen.findByRole("button", { name: "Next page" }))
    await screen.findByText("Older request")
    expect(inbox).toHaveBeenLastCalledWith(true, { pending_before: "req-1" })
    fireEvent.click(screen.getByRole("button", { name: "First page" }))
    await screen.findByText("Create a session")
  })
})
