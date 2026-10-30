import { afterEach, describe, expect, it, vi } from "vitest"
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import {
  filterSessions,
  summarize,
  type Session,
  type Snapshot,
  TranscriptError,
} from "./data"
import { TranscriptDashboard } from "./page"

const { read, refresh } = vi.hoisted(() => ({
  read: vi.fn(),
  refresh: { fn: () => {} },
}))
vi.mock("./data", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./data")>()),
  read,
}))
vi.mock("@tachyon/host-sdk", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tachyon/host-sdk")>()),
  dataRefreshRegistry: () => ({
    subscribe: (fn: () => void) => {
      refresh.fn = fn
      return () => {}
    },
  }),
}))
vi.mock("@hollis-labs/kit-dashboard/charts", () => ({
  TimeSeriesChart: () => <div>Activity chart</div>,
}))
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})
const base: Session = {
  id: "a".repeat(64),
  title: "Fix routing",
  tool: "codex",
  model: "gpt-test",
  kind: "main",
  project: "/work/tachyon",
  started_at: new Date().toISOString(),
  turns: 4,
  tokens: 600,
  tool_calls: 3,
  errors: 1,
}
const other: Session = {
  ...base,
  id: "b".repeat(64),
  title: "Design review",
  tool: "claude",
  model: "claude-test",
  kind: "subagent",
  errors: 0,
}
const snapshot: Snapshot = {
  status: { available: true, enabled: true, semantic: true, modes: ["text"] },
  facets: {
    total: 100,
    enriched: 40,
    project_count: 12,
    tools: ["codex", "claude"],
    models: ["gpt-test", "claude-test"],
  },
  sessions: [base, other],
  complete: false,
  limit: 200,
  observed_at: new Date().toISOString(),
}

describe("transcript calculations", () => {
  it("combines UTC date, tool, kind, error and metadata filters without inventing missing dates", () => {
    const filters = {
      days: 7,
      tool: "codex",
      model: "",
      kind: "main",
      query: "tachyon",
      errors: true,
    }
    expect(
      filterSessions(
        [base, other, { ...base, id: "undated", started_at: undefined }],
        filters,
      ).map((s) => s.id),
    ).toEqual([base.id])
    expect(
      filterSessions([{ ...base, started_at: undefined }], {
        ...filters,
        days: 0,
      }),
    ).toEqual([{ ...base, started_at: undefined }])
    expect(summarize([base, other])).toEqual({
      turns: 8,
      tokens: 1200,
      calls: 6,
      errors: 1,
      affected: 1,
    })
  })
})
describe("transcript dashboard", () => {
  it("separates database coverage from partial calculations and filters locally", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByText(/partial snapshot/)
    fireEvent.click(screen.getByRole("tab", { name: "Insights" }))
    expect(
      within(
        screen.getByRole("region", { name: "Database overview" }),
      ).getByText("100"),
    ).toBeTruthy()
    fireEvent.click(screen.getByRole("tab", { name: "Sessions" }))
    fireEvent.change(screen.getByLabelText("Tool"), {
      target: { value: "codex" },
    })
    expect(screen.getByRole("button", { name: "Fix routing" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Design review" })).toBeNull()
    fireEvent.change(screen.getByLabelText("Search session metadata"), {
      target: { value: "no-match" },
    })
    expect(await screen.findByText("No sessions match these filters.")).toBeTruthy()
  })
  it("loads a transcript on demand and renders Markdown with paged turns", async () => {
    read.mockImplementation((path: string) =>
      Promise.resolve(
        path === "/snapshot"
          ? snapshot
          : {
              session: base,
              turns: [
                { seq: 0, role: "assistant", text: "A **formatted** reply" },
              ],
              turn_total: 1,
            },
      ),
    )
    render(<TranscriptDashboard />)
    fireEvent.click(await screen.findByRole("button", { name: "Fix routing" }))
    expect((await screen.findByText("formatted")).tagName).toBe("STRONG")
    expect(read).toHaveBeenCalledWith(
      `/sessions/${base.id}?turn_offset=0`,
      false,
      expect.any(AbortSignal),
    )
    expect(
      (screen.getByRole("button", { name: "Next turns" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true)
  })
  it("removes prior private data when source authorization is revoked", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByRole("button", { name: "Fix routing" })
    read.mockRejectedValue(
      new TranscriptError(403, "authentication", "Access denied"),
    )
    refresh.fn()
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Fix routing" })).toBeNull(),
    )
    expect(screen.getByRole("alert").textContent).toContain("Access denied")
  })
  it("opens a deep-linked session and keeps the last snapshot when the source goes down", async () => {
    window.history.replaceState(null, "", `/?session=${base.id}`)
    read.mockImplementation((path: string) =>
      Promise.resolve(
        path === "/snapshot"
          ? snapshot
          : { session: base, turns: [], turn_total: 0 },
      ),
    )
    render(<TranscriptDashboard />)
    await waitFor(() =>
      expect(read).toHaveBeenCalledWith(
        `/sessions/${base.id}?turn_offset=0`,
        false,
        expect.any(AbortSignal),
      ),
    )
    fireEvent.keyDown(document.body, { key: "Escape" })
    window.history.replaceState(null, "", "/")
    read.mockRejectedValue(new Error("offline"))
    refresh.fn()
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Showing the last snapshot",
    )
    expect(screen.getByRole("button", { name: "Fix routing" })).toBeTruthy()
  })
  it("turns an Insights bar click into a visible, resettable filter on Sessions", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByText(/partial snapshot/)
    fireEvent.click(screen.getByRole("tab", { name: "Insights" }))
    fireEvent.click(await screen.findByText("claude"))
    expect(screen.getByText(/Filtered to claude/)).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Fix routing" })).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "Reset filters" }))
    expect(screen.getByRole("button", { name: "Fix routing" })).toBeTruthy()
  })
})
