import { toast } from 'sonner'
import { operatorElevation } from '@console/operator/elevation'
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
 * Runs a write; when the server asks for operator access, asks the person to authorize and runs the same write once
 * more (it was refused before anything changed). A declined prompt surfaces the original refusal.
 */
export async function authorizing<T>(write: () => Promise<T>): Promise<T> {
  try {
    return await write()
  } catch (err) {
    if (!needsOperator(err) || !(await operatorElevation.ensure().catch(() => false))) throw err
    return write()
  }
}
