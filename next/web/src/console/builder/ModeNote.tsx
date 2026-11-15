import { Button } from '@hollis-labs/design-components'

/** Builder is read-only when changes cannot be applied at all: one muted line, no toast. */
export function ModeNote({ onCheck }: { onCheck: () => void }) {
  return (
    <p className="px-5 py-2 dash-support text-fg-muted">
      Builder is read-only: changes cannot be applied from here right now.{' '}
      <Button type="button" variant="link" size="sm" onClick={onCheck}>
        Check again
      </Button>
    </p>
  )
}

export const PreparedLabel = () => (
  <span className="mr-auto flex items-center gap-2">
    <span className="h-1.5 w-1.5 rounded-full bg-fg-muted" aria-hidden />
    Prepared, not applied
  </span>
)
