import { describe, expect, it, vi } from "vitest"
import { needsOperator, PlannerApiError, plannerWriteMessage } from "./errors"
import { authorizing } from "./notifications"

// The port has no step-up (API contract section 5): a refused write is surfaced once, never replayed.
describe("a Planner write", () => {
  it("runs once and surfaces a refusal as it is", async () => {
    const refused = new PlannerApiError(403, "operator access required", "operator_required")
    const write = vi.fn().mockRejectedValue(refused)
    await expect(authorizing(write)).rejects.toBe(refused)
    expect(write).toHaveBeenCalledTimes(1)
    const ok = vi.fn().mockResolvedValue("ok")
    expect(await authorizing(ok)).toBe("ok")
  })

  it('treats operator and scope refusals as "may not write", and nothing else', () => {
    expect(needsOperator(new PlannerApiError(403, "m", "operator_required"))).toBe(true)
    expect(needsOperator(new PlannerApiError(403, "m", "operator_access_required"))).toBe(true)
    expect(
      needsOperator(
        new PlannerApiError(403, "required scope: tachyon.planner.write", "scope_required"),
      ),
    ).toBe(true)
    expect(needsOperator(new PlannerApiError(401, "m", "unauthorized"))).toBe(false)
    expect(needsOperator(new PlannerApiError(409, "m", "conflict"))).toBe(false)
    expect(needsOperator(new PlannerApiError(403, "m", "origin_denied"))).toBe(false)
  })

  it("explains each refusal without offering a step-up", () => {
    expect(plannerWriteMessage(new PlannerApiError(401, "m", "unauthorized"))).toMatch(
      /session has ended/,
    )
    expect(plannerWriteMessage(new PlannerApiError(403, "m", "operator_required"))).toMatch(
      /operator access/,
    )
    expect(plannerWriteMessage(new PlannerApiError(409, "m", "conflict"))).toMatch(/changed/)
    expect(plannerWriteMessage(new PlannerApiError(403, "m", "csrf"))).toMatch(/not permitted/)
  })
})
