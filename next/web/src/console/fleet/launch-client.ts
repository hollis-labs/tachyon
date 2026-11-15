import { hostFetch } from '@tachyon/host-sdk'
import { operatorFetch } from '../operator/elevation'

export interface LaunchProfile {
  id: string
  agent: string
  provider: string
  project: string
  enabled?: boolean
  display_name?: string
  worker?: string
}
export interface LaunchWorker { id: string; label: string; connected: boolean }
export interface TetherSession {
  id: string
  launch_id: string
  provider_id: string
  workspace: string
  state: string
  created_at: string
  project_id?: string
  workstream_id?: string
}
export interface LaunchInput { launch: string; worker?: string; prompt_append?: string; idempotency_key: string }
export interface LaunchAttempt { input: LaunchInput; sessionId?: string }
const BASE = '/api/plugins/console/v1/tether'
export class LaunchRequestError extends Error {
  constructor(message: string, public status: number, public code?: string) { super(message) }
}

async function json<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await operatorFetch(BASE + path, {
    ...init, headers: { Accept: 'application/json', 'Cache-Control': 'no-cache', ...init?.headers },
  })
  const body = await response.json().catch(() => null)
  if (!response.ok) {
    const code = body?.error?.code ?? body?.code
    const message = body?.error?.message ?? body?.message ?? (typeof body?.error === 'string' ? body.error : 'Tether request failed')
    throw new LaunchRequestError(`${message} (${response.status}${code ? ` · ${code}` : ''})`, response.status, code)
  }
  if (!body) throw new Error('Tether returned an invalid response.')
  return body as T
}
const post = (body: unknown): RequestInit => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })

export async function launchOptions() {
  const [response, workerResponse] = await Promise.all([
    hostFetch('/api/plugins/console/v1/catalog/launches', { cache: 'no-store', headers: { 'Cache-Control': 'no-cache' } }),
    json<{ workers: LaunchWorker[] }>('/workers'),
  ])
  if (!response.ok) throw new Error(`Launch profiles unavailable (${response.status}).`)
  const body = await response.json()
  if (!Array.isArray(body.launches) || !Array.isArray(workerResponse.workers)) throw new Error('Launch options returned an invalid response.')
  return { profiles: (body.launches as LaunchProfile[]).filter(p => p.enabled !== false), workers: workerResponse.workers }
}

/** Keep this attempt across failures: create is keyed and start is reconciled before retry. */
export async function launchAgent(attempt: LaunchAttempt): Promise<string> {
  if (!attempt.sessionId) {
    const created = await json<{ id: string }>('/sessions', post(attempt.input))
    if (!created.id || !/^[a-zA-Z0-9_-]{1,128}$/.test(created.id)) throw new Error('Tether did not return a session identity. Retry to recover the request.')
    attempt.sessionId = created.id
  }
  const path = `/sessions/${encodeURIComponent(attempt.sessionId)}`
  const session = await json<TetherSession>(path)
  if (session.state === 'running') return attempt.sessionId
  if (session.state !== 'created') throw new Error(`Session ${attempt.sessionId} is ${session.state}; it has not been launched again.`)
  try {
    await json(path + '/launch', post({}))
  } catch (error) {
    // The response may have been lost after Tether started the process.
    const current = await json<TetherSession>(path).catch(() => null)
    if (current?.state !== 'running') throw error
  }
  return attempt.sessionId
}

export async function runningSessions(): Promise<TetherSession[]> {
  const sessions: TetherSession[] = []
  const cursors = new Set<string>()
  let cursor = ''
  do {
    const page = await json<{ sessions: TetherSession[]; next_cursor?: string }>('/sessions' + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''))
    if (!Array.isArray(page.sessions)) throw new Error('Tether returned an invalid session list.')
    sessions.push(...page.sessions)
    cursor = page.next_cursor ?? ''
    if (cursor && cursors.has(cursor)) throw new Error('Tether session pagination did not advance.')
    cursors.add(cursor)
  } while (cursor)
  return sessions
}
