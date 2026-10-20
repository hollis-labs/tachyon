import type { ConsoleSource, SubscribeHandlers, SourceStatus } from '../events/source'
import { observations } from './store'

/** Polling adapter: shares the same bounded cache as the conversation indicators. */
export class TetherPollingSource implements ConsoleSource {
  readonly id = 'tether-poll'
  async snapshot() {
    await observations.refresh()
    const state = observations.getSnapshot()
    if (state.error) throw new Error(state.error)
    return { events: state.events, cursor: null }
  }
  subscribe(h: SubscribeHandlers) {
    let status: SourceStatus = 'connecting'
    h.onStatus(status)
    return observations.subscribe(() => {
      const state = observations.getSnapshot()
      const next = state.error ? 'error' : 'live'
      if (!state.error) h.onEvents(state.events)
      if (next !== status) { status = next; h.onStatus(next) }
    })
  }
}
