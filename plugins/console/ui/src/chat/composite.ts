import type { Recipient } from '../messaging'
import { isAppAgent, isAppThread } from './appagent'
import type { ChatClient, ChatThread } from './types'

/**
 * One ChatClient over two sources: the Tether-backed client (primary) and the
 * Tachyon-owned app agent. Calls route by thread id or agent address, so the UI
 * sees ordinary threads. A failing app agent never hides the primary threads.
 */
export function createCompositeChatClient(primary: ChatClient, app: ChatClient): ChatClient {
  let lastApp: ChatThread[] = [] // newest app agent threads we know of
  let lastPrimary: ChatThread[] = [] // newest Tether threads we know of
  const forThread = (id: string) => (isAppThread(id) ? app : primary)
  const merge = (a: ChatThread[], b: ChatThread[]) => [...a, ...b].sort((x, y) => y.lastAt - x.lastAt)
  return {
    async listThreads() {
      const [p, a] = await Promise.all([
        primary.listThreads(),
        // The app agent must never hide the Tether threads, but a failure must not be silent either.
        app.listThreads().catch((e) => {
          console.warn('App agent threads unavailable:', e instanceof Error ? e.message : e)
          return lastApp
        }),
      ])
      lastApp = a
      lastPrimary = p
      return merge(p, a)
    },
    openThread: (agent: Recipient) => (isAppAgent(agent) ? app : primary).openThread(agent),
    history: (id, q) => forThread(id).history(id, q),
    send: (id, body, opts) => forThread(id).send(id, body, opts),
    markRead: (id) => forThread(id).markRead(id),
    subscribe: (id, l) => forThread(id).subscribe(id, l),
    subscribeThreads(listener, onError) {
      // Seed both sides from the last full listing: with no poll interval the Tether side may not push again until a
      // spine frame, and an empty seed would leave only the app agent's thread in the list.
      let p: ChatThread[] | null = null
      // Start from what listThreads last saw: the Tether poll often fires first, and an empty
      // app list would make the Tachyon thread vanish until the app poll arrives.
      let a: ChatThread[] = lastApp
      // Until the Tether side pushes, use the newest full listing at the moment of each push (the first listing may still be in flight when we subscribe).
      const push = () => listener(merge(p ?? lastPrimary, a))
      const offP = primary.subscribeThreads((t) => { p = lastPrimary = t; push() }, onError)
      // The app agent is optional: its failures are not reported as chat errors.
      const offA = app.subscribeThreads((t) => { a = lastApp = t; push() })
      return () => { offP(); offA() }
    },
  }
}
