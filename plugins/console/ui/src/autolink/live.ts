import { hostFetch } from '@tachyon/host-sdk'
import { createPlannerResolver, createTorqueContainerResolver, epicEntity, projectEntity, sprintEntity, torquePlanEntity, createTetherAgentResolver, createTetherPlanResolver, createTorqueTaskResolver, PLANNER_ENTITIES, TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND, type KindResolver } from '../refs'
import type { TorqueApiClient } from '@tachyon/torque-client'
import { registerForgeRefs } from '../forge/refs'
import { registerTaskRefs } from './TaskDetailPane'
import { registerTesseractRefs } from './TesseractDetailPane'
import { createTesseractResolver } from './tesseractResolver'
import type { TesseractCache } from './tesseractDetail'

const tetherGet = (path: string, signal?: AbortSignal) => hostFetch(path, { signal, cache: 'no-store', headers: { Accept: 'application/json' } })

/**
 * Resolvers that talk to the real backend today: `torque.task`, through the
 * existing `/planner/torque-titles` batch endpoint, and the planner's own items
 * (themes, decisions, risks, inbox, workstreams) through `/planner/resolve`. Forge kinds (G2) resolve through the ForgeClient. Every other kind stays
 * unresolved (chip + link, no preview) until its endpoint exists; see BACKEND_NEEDS.md.
 * Memoise the result before handing it to AutolinkProvider.
 */
export function createLiveResolvers(api: Pick<TorqueApiClient, 'plannerTaskTitles' | 'plannerResolve'> & Partial<Pick<TorqueApiClient, 'getEpic' | 'getProject' | 'getSprint' | 'getPlan'>>, tesseract?: TesseractCache): Record<string, KindResolver> {
  // Forge kinds (G2): registers pr/commit/repo (idempotent) and resolves through the ForgeClient (mock by default).
  const forge = registerForgeRefs().resolvers
  registerTaskRefs()
  const planner = createPlannerResolver((ref, signal) => api.plannerResolve(ref, signal))
  const resolvers: Record<string, KindResolver> = {
    ...forge,
    // Operator-only reads; quiet on purpose, so a preview never raises the elevation prompt.
    'tether.plan': createTetherPlanResolver(tetherGet),
    'tether.agent': createTetherAgentResolver(tetherGet),
    'torque.task': createTorqueTaskResolver((ids, signal) => api.plannerTaskTitles(ids, signal)),
    // Containers: absent client methods (test doubles) leave the kind unresolved, as before.
    ...(api.getEpic ? { 'torque.epic': createTorqueContainerResolver((id) => api.getEpic!(id), epicEntity) } : {}),
    ...(api.getProject ? { 'torque.project': createTorqueContainerResolver((id) => api.getProject!(id), projectEntity) } : {}),
    ...(api.getSprint ? { 'torque.sprint': createTorqueContainerResolver((id) => api.getSprint!(id), sprintEntity) } : {}),
    ...(api.getPlan ? { 'torque.plan': createTorqueContainerResolver((id) => api.getPlan!(id), torquePlanEntity) } : {}),
    ...Object.fromEntries(PLANNER_ENTITIES.map((e) => [e.id, planner])),
  }
  if (tesseract) {
    // Read-only, through the console's Tesseract route; the same cache feeds the drawer.
    registerTesseractRefs()
    resolvers[TESSERACT_ITEM_KIND] = createTesseractResolver(TESSERACT_ITEM_KIND, tesseract)
    resolvers[TESSERACT_KEY_KIND] = createTesseractResolver(TESSERACT_KEY_KIND, tesseract)
  }
  return resolvers
}
