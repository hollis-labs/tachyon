// The port (next/web): QuickActions renders through the kit's Popover and Dialog (kit wins over carried UI).
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, expect, it } from "vitest"
import { createActionBus, MockIdentityConfirm } from "../seams"
import { createMockMessagingClient, MOCK_RECIPIENTS } from "../testkit/messaging"
import { MessagingProvider } from "./context"
import { AgentMessageActions } from "./QuickActions"

afterEach(cleanup)
const [pm] = MOCK_RECIPIENTS
const bus = () => createActionBus({ identity: new MockIdentityConfirm({ mode: "auto-grant" }) })

it("the nudge popover closes on Escape and gives focus back to its trigger", async () => {
  render(
    <MessagingProvider client={createMockMessagingClient()} actionBus={bus()}>
      <AgentMessageActions recipient={pm} />
    </MessagingProvider>,
  )
  const trigger = screen.getByRole("button", { name: "Nudge" })
  trigger.focus()
  fireEvent.click(trigger)
  const pop = await screen.findByRole("dialog", { name: "Nudge" })
  fireEvent.keyDown(pop, { key: "Escape" })
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Nudge" })).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(trigger))
})
