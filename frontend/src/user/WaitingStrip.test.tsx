import { cleanup, render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { afterEach, describe, expect, it } from "vitest"
import { WaitingStrip } from "./WaitingStrip"

afterEach(cleanup)

const ready = <T,>(data: T) => ({ status: "ready" as const, data })
const strip = (items: { critical: boolean }[] | null) =>
  render(
    <MemoryRouter>
      <WaitingStrip
        inbox={ready({ counts: { pending: 0 } })}
        alerts={ready({ needs_you: 0 })}
        needsYou={items === null ? { status: "error", message: "down" } : ready({ items })}
      />
    </MemoryRouter>,
  )

describe("WaitingStrip needs-you chip", () => {
  it("counts what waits on you and links to the needs-you view, marked when one is critical", () => {
    strip([{ critical: false }, { critical: true }])
    const chip = screen.getByRole("link", { name: /Needs you/ })
    expect(chip.getAttribute("href")).toBe("/needs-you")
    expect(chip.textContent).toContain("2")
    expect(chip.className).toContain("text-danger")
  })

  it("hides when nothing waits or the list is unavailable", () => {
    const { container } = strip([])
    expect(container.textContent).toBe("")
    cleanup()
    expect(strip(null).container.textContent).toBe("")
  })
})
