import { spineHub } from './live'
import { dataRefreshRegistry } from '@tachyon/host-sdk'
import type { Recipient } from '../messaging'
import { chatFetch, chatJSON, chatRecipient, fetchChatAgents, type ChatAgent } from './roster'
import type { ChatCapability, ChatClient, ChatEvent, ChatMessage, ChatThread, HistoryQuery, ReplyAudience } from './types'

const TURNS = '/api/plugins/console/v1/chat-turns'
export interface Turn {
  item_id: string
  agent_id: string
  session_id?: string
  turn_id?: string
  kind: string
  content: string
  created_at: string
  revision: number
  queue_sequence: number
  state: string
  delivery_state: string
  replyable?: boolean
  resolution?: { resolution_id: string; response_text?: string; resolved_at: string }
  source_message?: {
    origin?: string
    sender_urn?: string
    channel?: string
    message_id?: string
    sequence?: number
    output_id?: string
    /** Routed-message metadata. input_classes is comma-joined (or an array); empty unless Tether proved who asked. */
    attribution?: { launch_id?: string; runtime?: string; input_classes?: string | string[]; audience?: string }
  }
  stage_trace?: NonNullable<ChatMessage['info']>['trace']
}
interface Inbox {
  pending: Turn[]
  history: Turn[]
}
export interface Sent {
  key: string
  launch_id: string
  body: string
  at: number
  status: string
  error?: string
  session_id?: string
}
async function turns<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await chatFetch(TURNS + path, {
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
  const b = await res.json().catch(() => null)
  if (!res.ok) throw new Error(b?.message || `Conversation history unavailable (${res.status})`)
  return b as T
}
/**
 * Who asked for the turn that produced this reply, from Tether's proven
 * input_classes only. Never from the text, the channel or timestamps: a reply
 * with no classes (older, queued, shared-credential) is 'unknown'.
 */
export function replyAudience(t: Turn): ReplyAudience {
  const raw = t.source_message?.attribution?.input_classes
  const classes = (Array.isArray(raw) ? raw : typeof raw === 'string' ? raw.split(',') : []).map((c) => c.trim())
  if (classes.includes('operator')) return 'operator'
  const known = classes.filter((c): c is 'agent' | 'system' => c === 'agent' || c === 'system')
  return known.length === 1 && classes.length === 1 ? known[0] : 'unknown'
}
export function belongsTo(t: Turn, a: ChatAgent): boolean {
  const launch = t.source_message?.attribution?.launch_id
  return launch ? launch === a.launch_id : t.agent_id === a.agent_id
}
export function replyTarget(items: Turn[], a: ChatAgent): Turn | undefined {
  if (a.session?.state !== 'running') return undefined
  return items
    .filter(
      (t) =>
        belongsTo(t, a) &&
        t.session_id === a.session?.id &&
        (!t.resolution || t.delivery_state === 'retryable_failure') &&
        t.replyable !== false &&
        t.source_message?.origin !== 'publication' &&
        ['question', 'failure', 'terminal'].includes(t.kind) &&
        (['submitted', 'validated', 'staged', 'presented', 'in_progress'].includes(t.state) ||
          t.delivery_state === 'retryable_failure'),
    )
    .sort((a, b) => b.queue_sequence - a.queue_sequence)[0]
}
export function capability(a: ChatAgent, items: Turn[] = []): ChatCapability {
  const running = a.session?.state === 'running'
  const working = running && !a.session?.at_turn_boundary && !replyTarget(items, a)
  return {
    mode: 'session',
    sessionId: a.session?.id,
    canSend: !working,
    detail: working
      ? 'Working on a turn. Your draft is kept until the agent is ready.'
      : running
        ? 'Tether session chat. Ready for your next message.'
        : 'Tether chat. Sending starts a new session.',
  }
}
function replyDelivery(t: Turn): ChatMessage['delivery'] {
  if (t.delivery_state === 'acknowledged') return 'read'
  if (t.delivery_state === 'delivered') return 'delivered'
  if (t.delivery_state === 'terminal_failure' || t.delivery_state === 'retryable_failure') return 'failed'
  return 'queued'
}
export function messagesFor(a: ChatAgent, items: Turn[], sent: Sent[]): ChatMessage[] {
  const id = chatRecipient(a).address
  const out: ChatMessage[] = []
  for (const t of items.filter((t) => belongsTo(t, a))) {
    const src = t.source_message
    const info: ChatMessage['info'] = {
      transport: 'tether-chat',
      agentName: a.display_name,
      launchId: a.launch_id,
      sessionId: t.session_id,
      turnId: t.turn_id,
      kind: t.kind,
      delivery: t.delivery_state,
      sender: src?.sender_urn,
      channel: src?.channel,
      publicationId: src?.message_id,
      sequence: src?.sequence,
      outputId: src?.output_id,
      runtime: src?.attribution?.runtime,
      trace: t.stage_trace,
    }
    out.push({
      id: `turn:${t.item_id}`,
      threadId: id,
      from: 'agent',
      audience: replyAudience(t),
      body: t.content,
      at: Date.parse(t.created_at),
      tools: [],
      info,
    })
    const r = t.resolution
    if (r?.response_text) {
      const at = Date.parse(r.resolved_at)
      out.push({
        id: `reply:${t.item_id}`,
        threadId: id,
        from: 'operator',
        body: r.response_text,
        at,
        tools: [],
        delivery: replyDelivery(t),
        info,
      })
    }
  }
  for (const s of sent)
    out.push({
      id: `send:${s.key}`,
      threadId: id,
      from: 'operator',
      body: s.body,
      at: s.at,
      tools: [],
      delivery: s.status === 'sent' ? 'delivered' : 'failed',
      error:
        s.status === 'sent'
          ? undefined
          : s.error || 'Delivery was not confirmed. Retry this message with its original key.',
      info: {
        transport: 'tether-chat',
        launchId: a.launch_id,
        sessionId: s.session_id,
        idempotencyKey: s.key,
        delivery: s.status,
      },
    })
  return [...new Map(out.map((m) => [m.id, m])).values()].sort(
    (a, b) => a.at - b.at || a.id.localeCompare(b.id),
  )
}
const isChat = (id: string) => id.startsWith('chat://tether/launch/')
const launchOf = (id: string) => decodeURIComponent(id.slice('chat://tether/launch/'.length))

/** Compose session chat and the existing mailbox adapter without replacing either UI. */
export function createTetherChatClient(mail: ChatClient): ChatClient {
  const listeners = new Map<string, Set<(e: ChatEvent) => void>>()
  const drafts = new Map<string, { body: string; key: string; at: number; targetId?: string }>()
  const inFlight = new Set<string>()
  const confirmed = new Set<string>()
  const seen = new Map<string, number>()
  const emit = (id: string, e: ChatEvent) => listeners.get(id)?.forEach((fn) => fn(e))
  const agent = async (id: string, fresh = false) => {
    const a = (await fetchChatAgents(fresh)).find((a) => a.launch_id === launchOf(id))
    if (!a) throw new Error('This chat launch is no longer available')
    return a
  }
  const inbox = async (fresh = false) => {
    const b = await turns<Inbox>('/inbox', fresh ? { headers: { 'Cache-Control': 'no-cache' } } : undefined)
    if (!Array.isArray(b.pending) || !Array.isArray(b.history))
      throw new Error('Conversation history returned an invalid response')
    return [...b.pending, ...b.history]
  }
  const thread = (a: ChatAgent, items: Turn[] = []): ChatThread => {
    const messages = messagesFor(a, items, [])
    const last = messages.at(-1)
    const id = chatRecipient(a).address
    if (!seen.has(id)) seen.set(id, Date.now())
    return {
      id,
      agent: chatRecipient(a),
      title: a.display_name,
      capability: capability(a, items),
      unread: messages.filter((m) => m.from === 'agent' && m.at > (seen.get(id) ?? Date.now())).length,
      lastAt: last?.at ?? (Date.parse(a.session?.last_activity_at ?? '') || 0),
      lastPreview: last?.body.slice(0, 100),
    }
  }
  const history = async (id: string, q: HistoryQuery = {}) => {
    const [a, items, sent] = await Promise.all([
      agent(id),
      inbox(),
      chatJSON<Sent[]>('/outbox?launch_id=' + encodeURIComponent(launchOf(id))),
    ])
    const all = messagesFor(a, items, sent)
      .map((m) =>
        inFlight.has(m.id)
          ? { ...m, delivery: 'queued' as const, error: undefined }
          : confirmed.has(m.id)
            ? { ...m, delivery: 'delivered' as const, error: undefined }
            : m,
      )
      .filter((m) => q.before === undefined || m.at < q.before)
    emit(id, { type: 'capability', capability: capability(a, items) })
    const messages = all.slice(-(q.limit ?? 20))
    return { messages, hasMore: all.length > messages.length }
  }
  const client: ChatClient = {
    async listThreads() {
      const [agents, existing, items] = await Promise.all([fetchChatAgents(), mail.listThreads(), inbox()])
      return [...agents.map((a) => thread(a, items)), ...existing].sort((a, b) => b.lastAt - a.lastAt)
    },
    async openThread(a: Recipient) {
      return a.chatLaunchId || isChat(a.address) ? thread(await agent(a.address)) : mail.openThread(a)
    },
    history: (id, q) => (isChat(id) ? history(id, q) : mail.history(id, q)),
    async send(id, body) {
      if (!isChat(id)) return mail.send(id, body)
      // Fresh source state is required before deciding reply vs next turn/start.
      const [a, items] = await Promise.all([agent(id, true), inbox(true)])
      const cap = capability(a, items)
      emit(id, { type: 'capability', capability: cap })
      if (cap.canSend === false) throw new Error('The agent is working. Send again when the turn finishes.')
      let pending = drafts.get(id)
      if (pending?.body === body && pending.targetId) {
        const resolved = items.find(
          (t) =>
            t.item_id === pending?.targetId &&
            t.resolution?.response_text === body &&
            t.delivery_state !== 'retryable_failure',
        )
        if (resolved) {
          drafts.delete(id)
          return messagesFor(a, [resolved], []).find((m) => m.from === 'operator')!
        }
      }
      const target = replyTarget(items, a)
      if (!pending || pending.body !== body) {
        const sent = await chatJSON<Sent[]>('/outbox?launch_id=' + encodeURIComponent(a.launch_id), {
          headers: { 'Cache-Control': 'no-cache' },
        })
        const failed = sent.find((s) => s.body === body && s.status !== 'sent')
        pending = {
          body,
          key: failed?.key ?? crypto.randomUUID(),
          at: failed?.at ?? Date.now(),
          targetId: target?.item_id,
        }
        drafts.set(id, pending)
      }
      if (pending.targetId !== target?.item_id)
        throw new Error(
          'The conversation changed since this attempt. Review the latest messages before sending a new message.',
        )
      const message: ChatMessage = {
        id: target ? `reply:${target.item_id}` : `send:${pending.key}`,
        threadId: id,
        from: 'operator',
        body,
        at: pending.at,
        delivery: 'queued',
        tools: [],
        info: {
          transport: 'tether-chat',
          launchId: a.launch_id,
          sessionId: a.session?.id,
          idempotencyKey: pending.key,
        },
      }
      inFlight.add(message.id)
      emit(id, { type: 'message', message })
      try {
        let accepted: Turn | undefined
        if (target)
          accepted = await turns<Turn>('/items/' + encodeURIComponent(target.item_id) + '/reply', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              expected_revision: target.revision,
              action: 'respond',
              response_text: body,
            }),
          })
        else
          await chatJSON('/agents/' + encodeURIComponent(a.launch_id) + '/send', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ text: body, idempotency_key: pending.key }),
          })
        drafts.delete(id)
        if (!target) confirmed.add(message.id)
        const done: ChatMessage = {
          ...message,
          delivery: accepted ? replyDelivery(accepted) : 'delivered',
          info: { ...message.info!, delivery: accepted?.delivery_state ?? 'sent' },
        }
        emit(id, { type: 'message', message: done })
        return done
      } catch (e) {
        emit(id, {
          type: 'message',
          message: { ...message, delivery: 'failed', error: e instanceof Error ? e.message : 'Send failed' },
        })
        throw e
      } finally {
        inFlight.delete(message.id)
      }
    },
    async markRead(id) {
      if (!isChat(id)) return mail.markRead(id)
      seen.set(id, Date.now())
    },
    subscribe(id, listener) {
      if (!isChat(id)) return mail.subscribe(id, listener)
      const set = listeners.get(id) ?? new Set()
      set.add(listener)
      listeners.set(id, set)
      let live = true
      let running = false
      const sync = async () => {
        if (running) return
        running = true
        try {
          const p = await history(id, { limit: 20 })
          if (live) {
            listener({ type: 'error', message: null })
            for (const message of p.messages) listener({ type: 'message', message })
          }
        } catch (e) {
          if (live) listener({ type: 'error', message: e instanceof Error ? e.message : 'Chat unavailable' })
        } finally {
          running = false
        }
      }
      void sync()
      // No interval: refresh on the spine stream, the data-refresh signal and focus.
      const offSpine = spineHub().subscribe((f) => { if (f.type === 'thread' || f.type === 'reconnected') void sync() })
      const offs = ['chat', 'chat-turns'].map((scope) =>
        dataRefreshRegistry(scope).subscribe(() => void sync()),
      )
      window.addEventListener('focus', sync)
      return () => {
        live = false
        offSpine()
        offs.forEach((off) => off())
        window.removeEventListener('focus', sync)
        set.delete(listener)
      }
    },
    subscribeThreads(listener, onError) {
      let live = true
      const relist = () => void client.listThreads().then(
        (t) => { if (live) listener(t) },
        (e) => { if (live) onError?.(e instanceof Error ? e.message : 'Chat roster unavailable') },
      )
      // No interval: the spine stream says when a row changed, a reconnect or a refresh signal re-lists.
      const offSpine = spineHub().subscribe((f) => { if (f.type === 'thread' || f.type === 'reconnected') relist() })
      const offMail = mail.subscribeThreads(() => void client.listThreads().then(listener, () => undefined))
      const offRefresh = dataRefreshRegistry('chat').subscribe(relist)
      return () => {
        live = false
        offSpine()
        offMail()
        offRefresh()
      }
    },
  }
  return client
}
