import { useCallback, useEffect, useState } from 'react'
import { AGENTS_BASE } from './data'

export class AgentsApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message)
  }
}

async function call<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
  const r = await fetch(`${AGENTS_BASE}${path}`, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!r.ok) {
    const e = (await r.json().catch(() => ({}))) as { code?: string; message?: string }
    throw new AgentsApiError(r.status, e.code ?? 'internal', e.message ?? `The server answered ${r.status}`)
  }
  return (await r.json()) as T
}

export const agentsGet = <T>(path: string) => call<T>('GET', path)
export const agentsPost = <T>(path: string, body: unknown = {}) => call<T>('POST', path, body)

/** One read with loading, an inline error and Retry. Refetches when the path changes. */
export function useAgentsRead<T>(path: string | null) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = useCallback(async () => {
    if (!path) return
    try {
      setData(await agentsGet<T>(path))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load')
    }
  }, [path])
  useEffect(() => {
    setData(null)
    void load()
  }, [load])
  return { data, error, reload: load }
}

export interface ControlRequest {
  request_id: string
  kind: 'nudge' | 'restart'
  session_id: string
  state: string
  step: string | null
  error: string | null
  interrupt: string | null
  handoff?: boolean
  new_session_id?: string | null
  discarded_pending?: number | null
  wait_ends_at?: number | null
}

export interface AgentsSettings {
  stale_after_s: number
  handoff_prompt: string
  handoff_timeout_s: number
  nudge_presets: string[]
}
