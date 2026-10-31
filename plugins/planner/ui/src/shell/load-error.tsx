import { PlannerApiError } from '@planner/api/errors'
import { TEXT } from '@planner/lib/vocabulary'

/** What the person reads when a load fails: the server's own words when it sent some, otherwise a plain sentence (never "Failed to fetch"). */
export function loadMessage(err: unknown): string {
  return err instanceof PlannerApiError && err.message ? err.message : TEXT.loadFailedHint
}

/** A load that failed with nothing to show yet. */
export function LoadError({ title, message, onRetry }: { title: string; message: string; onRetry: () => void }) {
  return (
    <div role="alert" className="flex flex-col gap-1 px-3 py-4">
      <p className="text-control text-fg">{title}</p>
      <p className="text-caption text-fg-muted">{message}</p>
      <button type="button" className="self-start text-caption text-fg underline" onClick={onRetry}>{TEXT.retry}</button>
    </div>
  )
}
