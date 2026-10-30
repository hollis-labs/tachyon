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
