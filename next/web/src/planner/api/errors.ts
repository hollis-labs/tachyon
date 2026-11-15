export class PlannerApiError extends Error {
  constructor(public status: number, message: string, public code?: string, public details?: Record<string, unknown>) {
    super(message)
    this.name = 'PlannerApiError'
  }
}

export function plannerWriteMessage(error: unknown): string {
  if (error instanceof PlannerApiError) {
    if (error.status === 409 || error.code === 'conflict') return 'This record changed. Your draft is preserved. Reload the latest version and review your changes before submitting again.'
    if (error.code === 'origin_denied') return 'Open Tachyon at its authorized browser address to make changes. Your draft is preserved.'
    if (error.status === 401) return 'Your session has ended. Sign in again, then submit your change. Your draft is preserved.'
    if (needsOperator(error)) return 'Changing the planner needs operator access, which this account does not have. Your draft is preserved.'
    if (error.status === 403) return 'This operation is not permitted. Your draft is preserved.'
  }
  return error instanceof Error ? error.message : 'The change could not be confirmed. Your draft is preserved.'
}
/**
 * The account may not write (API contract section 3.1): it lacks the operator check or the route's scope. There is
 * no step-up; the shell turns writes off. `operator_access_required` is the old name, accepted until B3 lands.
 */
export function needsOperator(error: unknown): boolean {
  return error instanceof PlannerApiError && error.status === 403 && ['operator_required', 'operator_access_required', 'scope_required'].includes(error.code ?? '')
}
export function isConflict(error: unknown): boolean {
  return error instanceof PlannerApiError && (error.status === 409 || error.code === 'conflict')
}
