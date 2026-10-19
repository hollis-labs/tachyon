import { operatorFetch } from '../operator/elevation'

export interface FormationReceipt {
  id: string; object_id: string; object_revision: number; provider: string; target: string; verb: string
  payload: { team: { id: string; name: string; version: number; slots: { name: string; min: number; definition_ref: { id: string; revision: string; digest: string }; workspace?: { mission?: string } }[] } }
  digest: string; state: 'prepared' | 'sending' | 'unknown' | 'succeeded'; attempts: number; retry_after?: number
  result: { run?: { id: string; status: string; channel?: string }; members?: { id: string; slot: string; status: string }[] }
  problem?: string; created_at: string; updated_at: string
}
export type FormationSummary = Pick<FormationReceipt, 'id' | 'object_revision' | 'state' | 'created_at'>
export interface FormationPage { items: FormationSummary[]; next_before_id?: string }
export function createTeamActionsClient(fetcher: typeof fetch = operatorFetch) {
  async function call<T>(verb: string, input: unknown, write = false): Promise<T> {
    const res = await fetcher('/api/plugins/console/v1/data/providers/tether/verbs/' + verb + (write ? '' : '?input=' + encodeURIComponent(JSON.stringify(input))), {
      method: write ? 'POST' : 'GET', cache: 'no-store',
      headers: { Accept: 'application/json', ...(write ? { 'Content-Type': 'application/json' } : {}) },
      ...(write ? { body: JSON.stringify(input) } : {}),
    })
    const body = await res.json()
    if (!res.ok) throw new Error(typeof body.message === 'string' ? body.message : 'Could not confirm the action. Refresh its receipt before continuing.')
    if (body.version !== 1 || body.provider !== 'tether' || body.verb !== verb) throw new Error('Unexpected provider action response. Refresh the receipt before continuing.')
    return body.data as T
  }
  return {
    preview: (id: string, object_id: string, expected_revision: number, mission: string) => call<FormationReceipt>('teams.preview', { id, object_id, expected_revision, mission }, true),
    apply: (row: FormationReceipt, retry = false) => call<FormationReceipt>('teams.apply', { id: row.id, digest: row.digest, retry }, true),
    get: (id: string) => call<FormationReceipt>('actions.get', { id }),
    list: (object_id: string, before_id = '') => call<FormationPage>('actions.list', { object_id, before_id }),
  }
}
export type TeamActionsClient = ReturnType<typeof createTeamActionsClient>
