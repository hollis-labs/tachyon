import { toast } from 'sonner'
export function notifyError(error: unknown, fallback: string) {
  toast.error(error instanceof Error ? error.message : fallback)
}
export function notifySuccess(message: string) { toast.success(message) }
