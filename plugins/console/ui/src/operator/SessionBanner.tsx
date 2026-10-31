import { useEffect, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { operatorElevation } from "./elevation"

// Golden .expb: shown when /status reports session "expired"; drafts are kept.
export function SessionBanner() {
  const [expired, setExpired] = useState(false)
  useEffect(() => {
    let live = true
    const check = () =>
      void operatorElevation.expired().then((v) => live && setExpired(v))
    check()
    window.addEventListener("focus", check)
    return () => {
      live = false
      window.removeEventListener("focus", check)
    }
  }, [])
  if (!expired) return null
  return (
    <div
      role="status"
      className="flex flex-none items-center gap-2.5 border-b border-border-subtle bg-selection px-4 py-2 text-xs"
    >
      <b>Your session expired.</b> Sign in to continue. Drafts and queued comments are kept.
      <span className="flex-1" />
      <Button
        size="sm"
        onClick={() =>
          void operatorElevation.ensure().then((ok) => ok && setExpired(false))
        }
      >
        Sign in
      </Button>
    </div>
  )
}
