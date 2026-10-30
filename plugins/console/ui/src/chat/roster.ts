import { operatorFetch } from '../operator/elevation'
import { dataRefreshRegistry, hostFetch } from '@tachyon/host-sdk'
import { useEffect, useState } from 'react'
import type { Recipient } from '../messaging'

export interface ChatAgent {
  launch_id: string
  agent_id: string
  display_name: string
  channel: string
  worker?: string
  session: { id: string; state: string; last_activity_at: string; at_turn_boundary: boolean } | null
}
/**
 * Reads never raise the OPERATOR ACCESS dialog: only a write (send, answer, approve) asks for elevation, so
 * opening /chat shows the page and a refused read is an inline error instead of a modal over the stream.
 */
const quiet = (input: RequestInfo | URL, init?: RequestInit) => (init?.method ?? 'GET').toUpperCase() === 'GET' || String(input instanceof Request ? input.url : input).endsWith('/read')
export const chatFetch: typeof fetch = (input, init) => (quiet(input, init) ? hostFetch(input, init) : operatorFetch(input, init))
export const CHAT_BASE = '/api/plugins/console/v1/chat'
export async function chatJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await chatFetch(CHAT_BASE + path, {
    ...init,
    headers: { Accept: 'application/json', ...init?.headers },
  })
  if (
    new Headers(init?.headers).get('Cache-Control') === 'no-cache' &&
    ['stale', 'refreshing', 'cached'].includes(res.headers.get('X-Tachyon-Cache') ?? '')
  ) {
    throw new Error(
      'Could not verify the current conversation state. Nothing was sent; refresh and try again.',
    )
  }
  const body = await res.json().catch(() => null)
  if (!res.ok) {
    const detail =
      body?.error?.message ?? body?.message ?? (typeof body?.error === 'string' ? body.error : null)
    throw new Error(
      detail ??
        (res.status === 401 || res.status === 403
          ? 'Tether refused chat access. Check the host credential and permissions.'
          : `Chat request failed (${res.status})`),
    )
  }
  if (!body) throw new Error('Chat source returned an invalid response')
  return body as T
}
export async function fetchChatAgents(fresh = false): Promise<ChatAgent[]> {
  const data = await chatJSON<{ agents: ChatAgent[] }>(
    '/agents',
    fresh ? { headers: { 'Cache-Control': 'no-cache' } } : undefined,
  )
  if (!Array.isArray(data.agents)) throw new Error('Chat roster returned an invalid response')
  return data.agents.filter(
    (a) =>
      typeof a.launch_id === 'string' && typeof a.agent_id === 'string' && typeof a.display_name === 'string',
  )
}
export function chatRecipient(a: ChatAgent): Recipient {
  return {
    address: `chat://tether/launch/${encodeURIComponent(a.launch_id)}`,
    label: a.display_name,
    kind: 'agent',
    agentId: a.agent_id,
    chatLaunchId: a.launch_id,
  }
}
export function useChatRoster() {
  const [agents, setAgents] = useState<ChatAgent[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    let running = false
    const sync = async () => {
      if (running) return
      running = true
      try {
        const next = await fetchChatAgents()
        if (live) {
          setAgents(next)
          setError(null)
        }
      } catch (e) {
        if (live) setError(e instanceof Error ? e.message : 'Chat roster unavailable')
      } finally {
        running = false
      }
    }
    void sync()
    const timer = setInterval(() => void sync(), 5000)
    const off = dataRefreshRegistry('chat').subscribe(() => void sync())
    window.addEventListener('focus', sync)
    return () => {
      live = false
      clearInterval(timer)
      off()
      window.removeEventListener('focus', sync)
    }
  }, [])
  return { agents, error }
}
