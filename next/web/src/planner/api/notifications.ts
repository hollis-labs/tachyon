import { toast } from 'sonner'
import { needsOperator } from './errors'
export function notifyError(error: unknown, fallback: string) {
  toast.error(error instanceof Error ? error.message : fallback)
}
export function notifySuccess(message: string) { toast.success(message) }

/** A failed write. When the server wants operator access the shell turns writes off instead of toasting each time. */
export function notifyWriteError(error: unknown, fallback: string, onNeedsAccess?: () => void) {
  if (onNeedsAccess && needsOperator(error)) onNeedsAccess()
  else notifyError(error, fallback)
}

/**
 * Runs a write. The old build asked for a step-up and replayed a refused write; the port has no elevation (operator
 * is a scope the profile carries), so a refusal surfaces as it is. Kept so the callers stay unchanged.
 */
export async function authorizing<T>(write: () => Promise<T>): Promise<T> {
  return write()
}
