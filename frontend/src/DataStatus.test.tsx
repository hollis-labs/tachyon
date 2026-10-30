import type { DataFreshness } from "@tachyon/host-sdk"
import { render } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { describe, expect, it, vi } from "vitest"
import { DataStatus, freshnessSummary } from "./DataStatus"

const fresh = vi.hoisted(() => ({ rows: [] as unknown[] }))
vi.mock("@tachyon/host-sdk", async (orig) => ({
  ...(await orig<object>()),
  useDataFreshness: () => fresh.rows,
  refreshPageData: vi.fn(),
}))

const row: DataFreshness = {
  url: "/api/plugins/planner/v1/planner/board",
  page: "/planner",
  scope: "planner",
  state: "live",
  updatedAt: "2026-10-17T17:00:00.000Z",
  freshSeconds: 15,
  problem: null,
}
const now = Date.parse(row.updatedAt as string)

describe("freshness display", () => {
  it("ages a verified live snapshot into cached data", () => {
    expect(freshnessSummary([row], now).label).toBe("Live")
    expect(freshnessSummary([row], now + 16_000).label).toBe("Cached")
  })
  it("keeps outage and denied-access errors visible alongside older data", () => {
    const stale = freshnessSummary([{ ...row, state: "stale", problem: "unreachable" }], now)
    expect(stale.label).toBe("Stale data")
    expect(stale.problem).toContain("Cannot reach")
    expect(stale.updatedAt).toBe(row.updatedAt)
    const denied = freshnessSummary(
      [row, { ...row, state: "error", problem: "authentication" }],
      now,
    )
    expect(denied.label).toBe("Connection error")
    expect(denied.problem).toContain("Access denied")
  })
  it("reports the oldest constituent snapshot instead of hiding it behind a new read", () => {
    const latest = { ...row, updatedAt: "2026-10-17T17:00:05.000Z" }
    expect(freshnessSummary([row, latest], now + 5000).updatedAt).toBe(row.updatedAt)
  })
})

it("labels an imported snapshot honestly even alongside current live reads", () => {
  const imported = { ...row, state: "imported" as const, updatedAt: "2026-10-17T16:00:00.000Z" }
  const result = freshnessSummary([row, imported], now)
  expect(result.label).toBe("Imported snapshot")
  expect(result.updatedAt).toBe(imported.updatedAt)
  expect(
    freshnessSummary([imported, { ...row, state: "error", problem: "authentication" }], now).label,
  ).toBe("Connection error")
})

describe("status bar", () => {
  const bar = () =>
    render(
      <MemoryRouter initialEntries={["/planner"]}>
        <DataStatus />
      </MemoryRouter>,
    ).container
  it("is absent on a page that registers no data, not a permanent Connecting", () => {
    fresh.rows = []
    expect(bar().textContent).toBe("")
  })
  it("shows the state once the page has data", () => {
    fresh.rows = [row]
    expect(bar().textContent).toContain("Last updated")
  })
})
