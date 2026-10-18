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
    if (error.code === 'operator_session_required' || error.code === 'assurance_insufficient' || error.status === 401) return 'Operator authorization is required. Authorize, then submit your change again. Your draft is preserved.'
    if (error.status === 403) return 'This operation is not permitted. Your draft is preserved.'
  }
  return error instanceof Error ? error.message : 'The change could not be confirmed. Your draft is preserved.'
}
export function needsOperator(error: unknown): boolean {
  return error instanceof PlannerApiError && (error.status === 401 || ['operator_session_required', 'assurance_insufficient'].includes(error.code ?? ''))
}
export function isConflict(error: unknown): boolean {
  return error instanceof PlannerApiError && (error.status === 409 || error.code === 'conflict')
}
