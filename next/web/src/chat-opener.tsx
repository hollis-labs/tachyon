import { useEffect, useState } from "react"
import { ChatDockTrigger } from "./console/shell/chat-dock"

const MODAL = '[aria-modal="true"]'

/** True while a modal dialog (the shell's More sheet, a confirm) is open. */
function useModalOpen() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const check = () => setOpen(document.querySelector(MODAL) !== null)
    check()
    const watch = new MutationObserver(check)
    watch.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["aria-modal"],
    })
    return () => watch.disconnect()
  }, [])
  return open
}

/**
 * The chat dock's opener: the carried nav-rail trigger with its unread badge, drawn by the host outside the shell,
 * because app-shell 0.11.0's nav footer has no host slot (kit gap CW-20261115-0093; it returns to the rail footer on
 * the pin bump that ships one). From 1024px it sits in the rail's empty space, right-aligned above the footer group
 * (five footer links and Diagnostics, 218px); narrower, it floats at the right above the bottom tab bar (57px). It
 * steps aside while a modal is open, so it never sits over the More sheet. tests/dock.spec.ts checks that it covers
 * no shell control at 390px, 1023px, 1024px and 1440px.
 */
export function ChatOpener() {
  if (useModalOpen()) return null
  return (
    <div
      data-chat-opener=""
      className="fixed right-3 bottom-[calc(57px+env(safe-area-inset-bottom)+0.75rem)] z-20 border border-border-subtle bg-bg shadow-sm lg:right-auto lg:bottom-[234px] lg:left-[196px] lg:border-0 lg:shadow-none"
    >
      <ChatDockTrigger />
    </div>
  )
}
