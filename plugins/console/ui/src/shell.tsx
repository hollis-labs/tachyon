import { SlotProvider } from "@tachyon/host-sdk"
import { ApiProvider } from "@tachyon/torque-client"
import type { ReactNode } from "react"
import { AlertTrayTrigger, ConsoleShell } from "./alerts"
import { LinkTextSlot, ShellAutolink } from "./autolink"
import { consoleClient } from "./client"
import { ConsoleNavExtras as ShellNavExtras } from "./shell/chat-dock"

const consoleSlots = { LinkText: LinkTextSlot }

/** Whole-app layer: console providers, overlays, alert toasts, LinkText slot. */
export function ConsoleLayer({ children }: { children: ReactNode }) {
  return (
    <ApiProvider client={consoleClient}>
      <ShellAutolink>
        <SlotProvider slots={consoleSlots}>
          <ConsoleShell>{children}</ConsoleShell>
        </SlotProvider>
      </ShellAutolink>
    </ApiProvider>
  )
}

/** Nav rail footer extras (alerts tray trigger + chat dock trigger). */
export function ConsoleNavExtras() {
  return <ShellNavExtras alerts={<AlertTrayTrigger />} />
}
