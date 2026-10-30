import { useEffect, useRef } from "react"
import { AccessCard } from "./AccessCard"
import { approvalsApi } from "./approvalsApi"
import { DecisionsCard } from "./DecisionsCard"
import { DeviceCard } from "./DeviceCard"
import { ProfileCard } from "./ProfileCard"
import { useRemote } from "./useRemote"
import { WaitingStrip } from "./WaitingStrip"

export function UserPage() {
  const heading = useRef<HTMLHeadingElement>(null)
  const [inbox] = useRemote(approvalsApi.inbox)
  const [history] = useRemote(approvalsApi.history)
  const [access, reloadAccess] = useRemote(approvalsApi.access)
  useEffect(() => heading.current?.focus(), [])
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-12 shrink-0 items-center justify-between gap-3 border-b border-border-subtle px-4">
        <h1
          ref={heading}
          tabIndex={-1}
          className="font-mono text-caption font-semibold uppercase leading-none tracking-label text-fg outline-none"
        >
          You
        </h1>
        <span className="text-label text-fg-muted">Saved on this Tachyon</span>
      </header>
      <div className="flex min-h-0 flex-1 justify-center overflow-auto p-6 max-sm:p-3.5">
        <div className="flex w-full max-w-[720px] flex-col gap-3.5">
          <WaitingStrip inbox={inbox} />
          <ProfileCard elevated={access.status === "ready" && access.data.state === "elevated"} />
          <AccessCard state={access} reload={reloadAccess} />
          <DecisionsCard history={history} />
          <DeviceCard />
        </div>
      </div>
    </div>
  )
}
