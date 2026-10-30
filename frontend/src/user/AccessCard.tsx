import { Button } from "@hollis-labs/design-components"
import { useState } from "react"
import { Card } from "../settings/Card"
import { type Access, approvalsApi } from "./approvalsApi"
import { useRemote } from "./useRemote"

const CHIP = "rounded-full border border-border px-2 py-0.5 text-xs"
const ROW = "flex flex-wrap items-center justify-between gap-2 py-2"

export function minutesLeft(s?: number): string {
  const m = Math.max(1, Math.ceil((s ?? 0) / 60))
  return `${m} min left`
}

export function AccessCard() {
  const [state, reload] = useRemote<Access>(approvalsApi.access)
  const [revoking, setRevoking] = useState(false)
  const [error, setError] = useState("")

  return (
    <Card title="Access">
      {state.status === "loading" && (
        <div aria-busy="true" className="h-16 animate-pulse rounded-md bg-muted" />
      )}
      {state.status === "error" && (
        <div className="flex items-center justify-between gap-2" role="alert">
          <p className="text-sm text-text-subtle">Access could not be loaded.</p>
          <Button type="button" variant="outline" onClick={reload}>
            Retry
          </Button>
        </div>
      )}
      {state.status === "ready" && (
        <div className="divide-y divide-border-subtle">
          <div className={ROW}>
            <div>
              <p className="text-sm text-foreground">Current access</p>
              <p className="text-xs text-text-subtle">
                {state.data.state === "elevated"
                  ? "Operator elevated. Risky actions need no extra step until it ends."
                  : "Full access while sign-in is off. Risky actions only ask for a typed phrase."}
              </p>
            </div>
            <div className="flex items-center gap-2">
              <span className={CHIP}>
                {state.data.state === "elevated" ? "Operator elevated" : "Browser session"}
              </span>
              {state.data.state === "elevated" && (
                <span className={CHIP}>{minutesLeft(state.data.expires_in_s)}</span>
              )}
            </div>
          </div>
          {state.data.state === "elevated" && (
            <div className={ROW}>
              <p className="text-sm text-foreground">Operator access</p>
              <Button
                type="button"
                variant="outline"
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
          {error && (
            <p role="alert" className="py-2 text-xs text-danger">
              {error}
            </p>
          )}
          <div className={ROW}>
            <p className="text-sm text-foreground">Sign-in</p>
            <div className="flex items-center gap-2">
              <span className={CHIP}>Not set up</span>
              <Button type="button" variant="outline" disabled title="Available once sign-in ships">
                Set up
              </Button>
            </div>
          </div>
        </div>
      )}
    </Card>
  )
}
