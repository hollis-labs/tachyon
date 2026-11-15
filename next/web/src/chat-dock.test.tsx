import { ManifestShell } from "@hollis-labs/app-shell"
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"
import type { Recipient } from "./console/messaging"
import { FleetMessageActions } from "./console/messaging/FleetActions"
import {
  ChatDockGateway,
  ChatDockProvider,
  ChatDockTab,
  useOptionalChatDock,
} from "./console/shell/chat-dock"
import { manifest } from "./manifest"
import { actions } from "./shell-config"

// The dock body is the carried drawer (chat client, threads); here it only shows whom it was opened for.
vi.mock("./console/chat/ChatDock", () => ({
  ChatDock: ({ agent }: { agent?: Recipient | null }) => (
    <p>dock for {agent?.label ?? "the conversation list"}</p>
  ),
}))

const reviewer: Recipient = {
  address: "msg://agent/example/reviewer",
  label: "Reviewer",
  kind: "agent",
}

/** A chat agent as the fleet lists it (FleetActions' Chat opens its conversation). */
const fleetAgent = {
  id: "tether:sess-example",
  provider: "tether",
  chat: {
    launch_id: "L_example",
    agent_id: "example-builder",
    display_name: "Example builder",
    channel: "tether",
    session: null,
  },
}

/** Stands in for any other openChat caller: openChat with a recipient, from inside a page. */
function Opener() {
  const dock = useOptionalChatDock()
  return (
    <button type="button" onClick={() => dock?.openChat(reviewer)}>
      Chat with the reviewer {dock?.open ? "(open)" : ""}
    </button>
  )
}

function Page() {
  return (
    <>
      <ChatDockGateway />
      <Opener />
      <FleetMessageActions agent={fleetAgent} />
    </>
  )
}

function renderShell() {
  return render(
    <ChatDockProvider>
      <ManifestShell
        manifest={manifest}
        apiBase="/api"
        path="/planner"
        onNavigate={() => {}}
        pages={{ planner: Page }}
        views={{ "chat-dock": ChatDockTab }}
        actions={actions}
      />
    </ChatDockProvider>,
  )
}

/** The dock's close is reported after a tick (a StrictMode remount is not a close). */
const tick = () => act(() => new Promise((r) => setTimeout(r, 0)))

afterEach(cleanup)

/** The header toolbar's Chat entry (toolbar.primary in ui/app.json). */
const toolbarChat = () =>
  within(screen.getByRole("toolbar", { name: "Primary actions" })).getByRole("button", {
    name: "Chat",
  })

test("the header toolbar's Chat opens the dock on the conversation list", async () => {
  renderShell()
  expect(screen.queryByRole("dialog", { name: "Chat" })).toBeNull()
  await act(async () => {
    fireEvent.click(toolbarChat())
  })
  const dock = screen.getByRole("dialog", { name: "Chat" })
  expect(dock.textContent).toContain("dock for the conversation list")
  expect(screen.getByRole("button", { name: /Chat with the reviewer \(open\)/ })).toBeTruthy()
})

test("openChat opens the dock through the gateway with its recipient; a closed dock forgets it", async () => {
  renderShell()
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /Chat with the reviewer/ }))
  })
  expect(screen.getByRole("dialog", { name: "Chat" }).textContent).toContain("dock for Reviewer")
  await act(async () => {
    fireEvent.keyDown(document, { key: "Escape" })
  })
  expect(screen.queryByRole("dialog", { name: "Chat" })).toBeNull()
  await tick()
  expect(screen.getByRole("button", { name: "Chat with the reviewer" })).toBeTruthy()
  await act(async () => {
    fireEvent.click(toolbarChat())
  })
  expect(screen.getByRole("dialog", { name: "Chat" }).textContent).toContain(
    "dock for the conversation list",
  )
})

test("the manifest places the dock without diagnostics about it", () => {
  renderShell()
  expect(screen.getByRole("button", { name: /^Diagnostics/ }).textContent).toBe("Diagnostics")
})

test("FleetActions' Chat opens the dock on that agent's conversation", async () => {
  renderShell()
  const actionsRow = screen.getByRole("region", { name: "Message this agent" })
  await act(async () => {
    fireEvent.click(within(actionsRow).getByRole("button", { name: "Chat" }))
  })
  expect(screen.getByRole("dialog", { name: "Chat" }).textContent).toContain(
    "dock for Example builder",
  )
})
