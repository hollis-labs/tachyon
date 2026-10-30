import { useEffect, useRef } from "react"
import { DeviceCard } from "./DeviceCard"
import { ProfileCard } from "./ProfileCard"

export function UserPage() {
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => heading.current?.focus(), [])
  return (
    <div className="mx-auto max-w-[720px] space-y-4 p-4 md:p-6">
      <h1 ref={heading} tabIndex={-1} className="text-lg font-semibold text-foreground">
        You
      </h1>
      <ProfileCard />
      <DeviceCard />
    </div>
  )
}
