import { ApiError, createPluginTorqueClient } from '@tachyon/torque-client'
import type { PlannerClient } from './contracts'
import { PlannerApiError } from './errors'

/** Temporary, explicit legacy adapter. Removed only after coordinated cutover. */
export function createLegacyPlannerClient(): PlannerClient {
  const api = createPluginTorqueClient('planner', ['events'])
  const call = async <T,>(fn: () => Promise<T>): Promise<T> => {
    try { return await fn() } catch (err) {
      if (err instanceof ApiError) throw new PlannerApiError(err.status, err.message, err.code, err.details)
      throw err
    }
  }
  return {
    provider: 'torque',
    plannerDatabases: () => call(() => api.plannerDatabases()),
    plannerBoard: (opts, signal) => call(() => api.plannerBoard(opts, signal)),
    plannerSchema: db => call(() => api.plannerSchema(db)),
    plannerList: (db, opts, signal) => call(() => api.plannerList(db, opts, signal)),
    plannerGet: (db, id) => call(() => api.plannerGet(db, id)),
    plannerSearch: (q, db, signal) => call(() => api.plannerSearch(q, db, signal)),
    plannerCreate: (db, item) => call(() => api.plannerCreate(db, item)),
    plannerUpdate: (db, id, patch, rev) => call(() => api.plannerUpdate(db, id, patch, rev)),
    plannerComment: (db, id, text, _rev, kind) => call(() => api.plannerComment(db, id, text, kind)),
    plannerLinkAdd: (db, id, link) => call(() => api.plannerLinkAdd(db, id, link)),
    plannerLinkRemove: (db, id, kind, ref) => call(() => api.plannerLinkRemove(db, id, kind, ref)),
    plannerReorder: (db, items) => call(() => api.plannerReorder(db, items.map(it => it.id))),
    plannerDecide: (id, option, _rev, comment) => call(() => api.plannerDecide(id, option, comment)),
    plannerDefer: (id, _rev, note) => call(() => api.plannerDefer(id, note)),
    plannerReopen: id => call(() => api.plannerReopen(id)),
    plannerInboxPromote: (id, to, _rev, _targetId, fields) => call(() => api.plannerInboxPromote(id, to, fields)),
    plannerInboxDismiss: (id, _rev, note) => call(() => api.plannerInboxDismiss(id, note)),
    plannerTaskTitles: (ids, signal) => call(() => api.plannerTaskTitles(ids, signal)),
    plannerWorkstreamTasks: (id, signal) => call(() => api.plannerWorkstreamTasks(id, signal)),
    getTask: (id, signal) => call(() => api.getTask(id, signal)),
    listTaskComments: (id, query, signal) => call(() => api.listComments('task', id, query, signal)),
    resolveReference: async (db, id) => {
      if (!db) throw new PlannerApiError(400, 'This reference needs a Planner database.')
      return { db, id }
    },
    subscribeEvents: listener => api.subscribeEvents(listener),
    enable: async () => { await call(() => api.setSetting('features.planner', 'true')) },
  }
}
