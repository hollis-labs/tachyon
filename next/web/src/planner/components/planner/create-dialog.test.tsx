// Carried from plugins/planner/ui owned-planner.test.tsx: the create-dialog case. The rest of that file covered
// the detail dialog, which only the unrouted PlannerPage used and the port does not carry.

import { TachyonPlannerClient } from "@planner/api/client"
import { PlannerApiProvider } from "@planner/api/context"
import type { PlannerSchemaInfo } from "@planner/api/types"
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { PlannerCreateDialog } from "./planner-create-dialog"

const schema: PlannerSchemaInfo = {
  db: "ideas",
  id: "owned/ideas@1",
  description: "",
  required: ["title"],
  fields: { title: { type: "string" }, notes: { type: "string" } },
  enums: {},
  link_kinds: ["url", "torque", "planner"],
}
function client() {
  return Object.assign(new TachyonPlannerClient(), {
    plannerSchema: vi.fn().mockResolvedValue(schema),
    plannerTaskTitles: vi.fn().mockResolvedValue({}),
  })
}
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe("creating a Planner item", () => {
  it("retains the create UUID and draft after an uncertain network outcome", async () => {
    const api = client()
    const create = vi
      .spyOn(api, "plannerCreate")
      .mockRejectedValue(new TypeError("Network disconnected"))
    render(
      <PlannerApiProvider client={api}>
        <PlannerCreateDialog db="ideas" open onOpenChange={vi.fn()} onCreated={vi.fn()} />
      </PlannerApiProvider>,
    )
    await screen.findByLabelText(/^Title/)
    fireEvent.change(screen.getByLabelText(/^Title/), { target: { value: "New idea" } })
    fireEvent.click(screen.getByRole("button", { name: "Create idea" }))
    // findBy's default 1s was too tight on loaded CI runners (file took ~3s).
    await screen.findByRole("alert", {}, { timeout: 5000 })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Create idea" }))
    })
    expect(create).toHaveBeenCalledTimes(2)
    expect(create.mock.calls[0]).toEqual(create.mock.calls[1])
    expect(create.mock.calls[0][2]).toMatch(/^[0-9a-f-]{36}$/)
    // The inner wait alone may take 5s on a loaded runner; the test's own budget must be larger.
  }, 15_000)
})
