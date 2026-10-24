import type { Recipient } from '../messaging'
import { isAppAgent, isAppThread } from './appagent'
import type { ChatClient, ChatThread } from './types'

/**
 * One ChatClient over two sources: the Tether-backed client (primary) and the
 * Tachyon-owned app agent. Calls route by thread id or agent address, so the UI
 * sees ordinary threads. A failing app agent never hides the primary threads.
 */
export function createCompositeChatClient(primary: ChatClient, app: ChatClient): ChatClient {
  const forThread = (id: string) => (isAppThread(id) ? app : primary)
  const merge = (a: ChatThread[], b: ChatThread[]) => [...a, ...b].sort((x, y) => y.lastAt - x.lastAt)
  return {
    async listThreads() {
      const [p, a] = await Promise.all([primary.listThreads(), app.listThreads().catch(() => [] as ChatThread[])])
      return merge(p, a)
    },
    openThread: (agent: Recipient) => (isAppAgent(agent) ? app : primary).openThread(agent),
    history: (id, q) => forThread(id).history(id, q),
    send: (id, body, opts) => forThread(id).send(id, body, opts),
    markRead: (id) => forThread(id).markRead(id),
    subscribe: (id, l) => forThread(id).subscribe(id, l),
    subscribeThreads(listener, onError) {
      let p: ChatThread[] = []
      let a: ChatThread[] = []
      const push = () => listener(merge(p, a))
      const offP = primary.subscribeThreads((t) => { p = t; push() }, onError)
      // The app agent is optional: its failures are not reported as chat errors.
      const offA = app.subscribeThreads((t) => { a = t; push() })
      return () => { offP(); offA() }
    },
  }
}
