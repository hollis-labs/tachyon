import { operatorFetch } from '../operator/elevation'

export interface ActionReceipt {
  id: string; object_id: string; object_revision: number; target: string; verb: string
  payload: Record<string, unknown>; digest: string; origin_action_id?: string
  state: 'prepared' | 'sending' | 'unknown' | 'succeeded'; attempts: number; retry_after?: number
  result: Record<string, unknown>; problem?: string; created_at: string; updated_at: string
}
export type ActionSummary = Pick<ActionReceipt, 'id' | 'verb' | 'object_revision' | 'state' | 'created_at'>
export interface RosterSnapshot {
  runs: { run: { id: string; status: string }; roster_version: number; members: { id: string; slot: string; status: string; actor: string; kind: string }[] }[]
  observed_at?: string; problem?: string
}
export function createProviderActionsClient(fetcher: typeof fetch = operatorFetch) {
  async function call<T>(verb: string, input: unknown, write = false): Promise<T> {
    const res = await fetcher('/api/plugins/console/v1/data/providers/tether/verbs/' + verb + (write ? '' : '?input=' + encodeURIComponent(JSON.stringify(input))), {
      method: write ? 'POST' : 'GET', cache: 'no-store',
      headers: { Accept: 'application/json', ...(write ? { 'Content-Type': 'application/json' } : {}) },
      ...(write ? { body: JSON.stringify(input) } : {}),
    })
    const body = await res.json()
    if (!res.ok) throw new Error(typeof body.message === 'string' ? body.message : 'Request not confirmed. Check its retained receipt before continuing.')
    if (body.version !== 1 || body.provider !== 'tether' || body.verb !== verb) throw new Error('Unexpected provider response. Check the retained receipt.')
    return body.data as T
  }
  return {
    runtime: (input: { id: string; object_id: string; expected_revision: number; formation_id: string; operation: string; address?: string; body?: string; slot?: string; member_id?: string; cascade?: boolean }) => call<ActionReceipt>('runtime.preview', input, true),
    publish: (input: { id: string; object_id: string; expected_revision: number; provider: string; extends: string }) => call<ActionReceipt>('agents.publish_preview', input, true),
    installation: (id: string, plan_action_id: string) => call<ActionReceipt>('catalog.apply_preview', { id, plan_action_id }, true),
    apply: (row: ActionReceipt, retry = false) => call<ActionReceipt>('actions.apply', { id: row.id, digest: row.digest, retry }, true),
    reconcile: (id: string) => call<ActionReceipt>('actions.reconcile', { id }, true),
    get: (id: string) => call<ActionReceipt>('actions.get', { id }),
    list: (object_id: string, before_id = '', verb = '') => call<{ items: ActionSummary[]; next_before_id?: string }>('actions.list', { object_id, before_id, verb }),
    roster: (formation_id: string, refresh = false) => call<RosterSnapshot>('teams.roster', { formation_id, refresh }),
  }
}
export type ProviderActionsClient = ReturnType<typeof createProviderActionsClient>
export const providerActionsClient = createProviderActionsClient()
