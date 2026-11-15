import { Button } from "@hollis-labs/design-components"
import { isConflict, plannerWriteMessage } from "@planner/api/errors"

/** A refused write. No elevation in the port: an operator refusal is explained, not authorized from here. */
export function PlannerWriteError({ error, onReload }: { error: unknown; onReload?: () => void }) {
  if (!error) return null
  return (
    <div
      role="alert"
      className="flex flex-col gap-2 border border-warning/40 bg-warning/5 p-3 dash-body"
    >
      <p>{plannerWriteMessage(error)}</p>
      <div className="flex gap-2">
        {isConflict(error) && onReload && (
          <Button size="sm" variant="outline" onClick={onReload}>
            Reload latest version
          </Button>
        )}
      </div>
    </div>
  )
}
