import { Button, Skeleton } from "@hollis-labs/design-components"
import { useState } from "react"
import { Chip, SetCard, SetRow } from "../settings/parts"
import { type Access, approvalsApi } from "./approvalsApi"
import type { Remote } from "./useRemote"

export function minutesLeft(s?: number): string {
  const m = Math.max(1, Math.ceil((s ?? 0) / 60))
  return `${m} min left`
}

export function AccessCard({ state, reload }: { state: Remote<Access>; reload: () => void }) {
  const [revoking, setRevoking] = useState(false)
  const [error, setError] = useState("")
  const elevated = state.status === "ready" && state.data.state === "elevated"

  return (
    <SetCard>
      <SetRow title="Access" help="What this browser can do right now.">
        {state.status === "ready" && (
          <Chip tone={elevated ? "strong" : undefined}>
            {elevated ? "Operator elevated" : "Browser session"}
          </Chip>
        )}
      </SetRow>
      {state.status === "loading" && (
        <div
          role="status"
          aria-busy="true"
          aria-label="Loading access"
          className="flex flex-col gap-2 border-t border-border-subtle px-4 py-3"
        >
          <Skeleton className="h-3.5 w-28" />
          <Skeleton className="h-3 w-64" />
        </div>
      )}
      {state.status === "error" && (
        <div
          role="alert"
          className="flex items-center justify-between gap-2 border-t border-border-subtle px-4 py-3"
        >
          <p className="text-xs text-fg-muted">Access could not be loaded.</p>
          <Button type="button" variant="outline" size="sm" onClick={reload}>
            Retry
          </Button>
        </div>
      )}
      {state.status === "ready" && (
        <SetRow
          sub
          title="Current access"
          help={
            elevated
              ? "Operator elevated. Risky actions need no extra step until it ends."
              : "Full access while sign-in is off. Risky actions only ask for a typed phrase."
          }
        >
          {elevated && (
            <div className="flex items-center gap-2">
              <Chip>{minutesLeft(state.data.expires_in_s)}</Chip>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={revoking}
                onClick={async () => {
                  setRevoking(true)
                  setError("")
                  try {
                    await approvalsApi.deElevate()
                    reload()
                  } catch (e) {
                    setError(e instanceof Error ? e.message : "Could not revoke.")
                  } finally {
                    setRevoking(false)
                  }
                }}
              >
                {revoking ? "Revoking" : "Revoke elevation"}
              </Button>
            </div>
          )}
        </SetRow>
      )}
      {error && (
        <p role="alert" className="border-t border-border-subtle px-4 py-2 text-xs text-danger">
          {error}
        </p>
      )}
      <SetRow sub title="Sign-in" help="Password and passkey for this account.">
        <div className="flex items-center gap-2">
          <Chip>Not set up</Chip>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled
            title="Available once sign-in ships"
          >
            Set up
          </Button>
        </div>
      </SetRow>
    </SetCard>
  )
}
