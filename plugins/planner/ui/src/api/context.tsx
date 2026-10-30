import { createApiContext, useSSE } from '@hollis-labs/design-app-runtime'
import { useEffect, useState } from 'react'
import type { PlannerClient, PlannerEvent } from './contracts'
import { TachyonPlannerClient } from './client'

const context = createApiContext<PlannerClient>(new TachyonPlannerClient())
export const PlannerApiProvider = context.ApiProvider
export const usePlannerApi = context.useApi
const taskEvents = ['task.created', 'task.updated', 'task.transitioned']
/** What the Planner stream sends: a signal to refetch, never state. `resync` means events were missed. */
export const PLANNER_STREAM_EVENTS = ['planner.changed', 'planner.item', 'planner.event', 'planner.task', 'planner.need_you', 'resync']

export function usePlannerEvents() {
  const api = usePlannerApi()
  const [lastEvent, setLastEvent] = useState<PlannerEvent | null>(null)
  const stream = useSSE(typeof EventSource === 'undefined' ? null : api.eventsUrl ?? null, { events: PLANNER_STREAM_EVENTS })
  const execution = useSSE(typeof EventSource !== 'undefined' && api.provider === 'tachyon' ? '/api/plugins/console/stream/events' : null, { events: taskEvents })
  useEffect(() => { if (stream.lastEvent) setLastEvent(stream.lastEvent) }, [stream.lastEvent])
  useEffect(() => { if (execution.lastEvent && taskEvents.includes(execution.lastEvent.type)) setLastEvent(execution.lastEvent) }, [execution.lastEvent])
  useEffect(() => {
    if (stream.connected && api.provider === 'tachyon') return
    return api.subscribeEvents(event => {
      if (PLANNER_STREAM_EVENTS.includes(event.type) || ['task.created', 'task.updated', 'task.transitioned'].includes(event.type)) setLastEvent(event)
    })
  }, [api, stream.connected])
  return { lastEvent }
}
