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
import { dayLabel, groupByDay } from "./sessions"

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
  window.history.replaceState(null, "", "/")
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
describe("session list", () => {
  const noon = new Date(2026, 9, 30, 12).getTime()
  const at = (d: number, h = 9) => new Date(2026, 9, d, h).toISOString()
  it("groups newest first by local day with Today, Yesterday and dated headers", () => {
    expect(dayLabel(at(30), noon)).toBe("Today")
    expect(dayLabel(at(29), noon)).toBe("Yesterday")
    const groups = groupByDay(
      [
        { ...base, id: "old", started_at: at(28) },
        { ...base, id: "undated", started_at: undefined },
        { ...base, id: "new", started_at: at(30, 8) },
        { ...base, id: "newer", started_at: at(30, 11) },
      ],
      noon,
    )
    expect(groups.map((g) => [g.label, g.rows.map((r) => r.id)])).toEqual([
      ["Today", ["newer", "new"]],
      [dayLabel(at(28), noon), ["old"]],
      ["Undated", ["undated"]],
    ])
  })
  it("treats a one-day window as the last 24 hours", () => {
    const now = Date.parse("2026-10-30T12:00:00Z")
    const f = { days: 1, tool: "", model: "", kind: "", query: "", errors: false }
    const rows = [
      { ...base, id: "in", started_at: "2026-10-29T13:00:00Z" },
      { ...base, id: "out", started_at: "2026-10-29T11:00:00Z" },
    ]
    expect(filterSessions(rows, f, now).map((r) => r.id)).toEqual(["in"])
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
    expect(screen.getByRole("button", { name: /Design review/ })).toBeTruthy()
    fireEvent.change(screen.getByLabelText("Search session metadata"), {
      target: { value: "routing" },
    })
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /Design review/ })).toBeNull(),
    )
    expect(screen.getByRole("button", { name: /Fix routing/ })).toBeTruthy()
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
                { seq: 0, role: "user", text: "Hi" },
                { seq: 1, role: "assistant", text: "A **formatted** reply" },
              ],
              turn_total: 2,
            },
      ),
    )
    render(<TranscriptDashboard />)
    fireEvent.click(await screen.findByRole("button", { name: /Fix routing/ }))
    expect((await screen.findByText("formatted")).tagName).toBe("STRONG")
    expect(read).toHaveBeenCalledWith(
      `/sessions/${base.id}?turn_offset=0`,
      false,
      expect.any(AbortSignal),
    )
    // One "You" in the speaker filter, one on the turn itself.
    expect(screen.getAllByText("You")).toHaveLength(2)
    expect(screen.queryByRole("button", { name: "Load more turns" })).toBeNull()
    expect(window.location.search).toBe(`?session=${base.id}`)
    fireEvent.keyDown(document.body, { key: "Escape" })
    await waitFor(() =>
      expect(screen.queryByLabelText("Transcript reader")).toBeNull(),
    )
    expect(window.location.search).toBe("")
    expect(
      screen.getByRole("button", { name: /Fix routing/ }).getAttribute("aria-current"),
    ).toBe("true")
  })
  it("finds text in loaded turns with n of m and filters by speaker", async () => {
    read.mockImplementation((path: string) =>
      Promise.resolve(
        path === "/snapshot"
          ? snapshot
          : {
              session: base,
              turns: [
                { seq: 0, role: "user", text: "alpha one" },
                { seq: 1, role: "assistant", text: "beta" },
                { seq: 2, role: "assistant", text: "alpha two" },
              ],
              turn_total: 3,
            },
      ),
    )
    render(<TranscriptDashboard />)
    fireEvent.click(await screen.findByRole("button", { name: /Fix routing/ }))
    fireEvent.change(await screen.findByLabelText("Find in transcript"), {
      target: { value: "alpha" },
    })
    expect(await screen.findByText("1 of 2")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Next match" }))
    expect(screen.getByText("2 of 2")).toBeTruthy()
    fireEvent.click(screen.getByRole("tab", { name: "You" }))
    expect(screen.queryByText("alpha two")).toBeNull()
    expect(await screen.findByText("1 of 1")).toBeTruthy()
  })
  it("says when nothing is recorded and offers one action when the source is not connected", async () => {
    read.mockResolvedValue({ ...snapshot, sessions: [] })
    const first = render(<TranscriptDashboard />)
    expect(await screen.findByText("No transcripts have been recorded yet.")).toBeTruthy()
    first.unmount()
    read.mockRejectedValue(new TranscriptError(403, "authentication", "Access denied"))
    render(<TranscriptDashboard />)
    expect((await screen.findAllByText("Access denied")).length).toBeGreaterThan(0)
    expect(screen.queryByText("Connect the transcript source to explore sessions.")).toBeNull()
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull()
  })
  it("asks for operator access when the session is required and shows nothing else", async () => {
    read.mockRejectedValue(new TranscriptError(401, "session_required", "Connect the approval queue first."))
    render(<TranscriptDashboard />)
    expect(await screen.findByText("Operator access needed")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Connect" })).toBeTruthy()
    expect(screen.queryByText("Waiting for metadata")).toBeNull()
  })
  it("focuses transcript find on Ctrl+F only inside the reader", async () => {
    read.mockImplementation((path: string) =>
      Promise.resolve(
        path === "/snapshot" ? snapshot : { session: base, turns: [], turn_total: 0 },
      ),
    )
    render(<TranscriptDashboard />)
    fireEvent.click(await screen.findByRole("button", { name: /Fix routing/ }))
    const find = await screen.findByLabelText("Find in transcript")
    fireEvent.keyDown(screen.getByLabelText("Transcript reader"), { key: "f", ctrlKey: true })
    expect(document.activeElement).toBe(find)
  })
  it("shows tool calls as plain lines, expands an error excerpt, and steps through failed turns", async () => {
    read.mockImplementation((path: string) =>
      Promise.resolve(
        path === "/snapshot"
          ? snapshot
          : {
              session: base,
              turns: [
                { seq: 0, role: "assistant", text: "ran it", tool_calls: [{ name: "Bash", status: "ok", result_bytes: 2048, started_at: 1000, ended_at: 3500 }] },
                { seq: 1, role: "assistant", text: "failed one", tool_calls: [{ name: "Edit", status: "error", target: "src/a.ts", error_excerpt: "<b>boom</b>" }] },
                { seq: 2, role: "assistant", text: "failed two", tool_calls: [{ name: "Bash", status: "error" }] },
              ],
              turn_total: 3,
            },
      ),
    )
    render(<TranscriptDashboard />)
    fireEvent.click(await screen.findByRole("button", { name: /Fix routing/ }))
    expect(await screen.findByText(/2\.0 KB/)).toBeTruthy()
    expect(screen.getByText(/2\.5 s/)).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Show error" }))
    // Untrusted text is shown as text, never parsed as markup.
    expect(screen.getByText("<b>boom</b>")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Next error" }))
    expect(screen.getByText("1 of 2 failed calls")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Next error" }))
    expect(screen.getByText("2 of 2 failed calls")).toBeTruthy()
  })
  it("opens a valid deep-linked id outside the snapshot and says when it is not found", async () => {
    const id = "c".repeat(64)
    window.history.replaceState(null, "", `/?session=${id}`)
    read.mockImplementation((path: string) =>
      path === "/snapshot"
        ? Promise.resolve(snapshot)
        : Promise.reject(new TranscriptError(404, "not_found", "No such session")),
    )
    render(<TranscriptDashboard />)
    expect(await screen.findByText("This session was not found.")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "Back to sessions" }))
    await waitFor(() => expect(screen.queryByLabelText("Transcript reader")).toBeNull())
  })
  it("does not request /status while the backend has no such route (TR1)", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByText(/partial snapshot/)
    expect(read.mock.calls.map((c) => c[0])).not.toContain("/status")
  })
  // Re-enable with useSearchStatus when TR1 ships /transcripts/status.
  it.skip("offers content search only when the bridge says it exists and shows snippets as text", async () => {
    read.mockImplementation((path: string) => {
      if (path === "/snapshot") return Promise.resolve(snapshot)
      if (path === "/status")
        return Promise.resolve({ modes: ["text"], capabilities: { search: true } })
      if (path.startsWith("/search?"))
        return Promise.resolve({
          items: [{ ...base, snippet_unused: 1, match: { snippet: "go test for the [forge] <i>x</i>", highlight: { format: "brackets" } } }],
          next_cursor: null,
          notice: { message: "Semantic search is unavailable; showing text results." },
        })
      return Promise.reject(new Error("unexpected " + path))
    })
    render(<TranscriptDashboard />)
    fireEvent.click(await screen.findByRole("tab", { name: "Content" }))
    fireEvent.change(screen.getByLabelText("Search transcript content"), { target: { value: "forge" } })
    fireEvent.click(screen.getByRole("button", { name: "Search" }))
    expect((await screen.findByText("forge")).tagName).toBe("STRONG")
    expect(screen.getByText(/<i>x<\/i>/)).toBeTruthy()
    expect(screen.getByText(/Semantic search is unavailable/)).toBeTruthy()
    expect(window.location.search).not.toContain("forge")
  })
  it("hides content search when the bridge does not advertise it", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByText(/partial snapshot/)
    expect(screen.queryByRole("tab", { name: "Content" })).toBeNull()
  })
  it("removes prior private data when source authorization is revoked", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByRole("button", { name: /Fix routing/ })
    read.mockRejectedValue(
      new TranscriptError(403, "authentication", "Access denied"),
    )
    refresh.fn()
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /Fix routing/ })).toBeNull(),
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
      "Showing the last saved snapshot",
    )
    expect(screen.getByRole("button", { name: /Fix routing/ })).toBeTruthy()
  })
  it("turns an Insights bar click into a visible, resettable filter on Sessions", async () => {
    read.mockResolvedValue(snapshot)
    render(<TranscriptDashboard />)
    await screen.findByText(/partial snapshot/)
    fireEvent.click(screen.getByRole("tab", { name: "Insights" }))
    // Skip the filter's <option>; the Tools panel bar is the first other match.
    const bar = (await screen.findAllByText("claude")).find(
      (el) => el.tagName !== "OPTION",
    )!
    fireEvent.click(bar)
    expect(screen.getByText(/Filtered to claude/)).toBeTruthy()
    expect(screen.queryByRole("button", { name: /Fix routing/ })).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "Reset filters" }))
    expect(screen.getByRole("button", { name: /Fix routing/ })).toBeTruthy()
  })
})
