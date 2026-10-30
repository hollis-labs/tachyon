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
  useEffect(() => heading.current?.focus(), [])
  return (
    <div className="mx-auto max-w-[720px] space-y-4 p-4 md:p-6">
      <h1 ref={heading} tabIndex={-1} className="text-lg font-semibold text-foreground">
        You
      </h1>
      <WaitingStrip inbox={inbox} />
      <ProfileCard />
      <AccessCard />
      <DecisionsCard history={history} />
      <DeviceCard />
    </div>
  )
}
