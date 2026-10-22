import { createTorqueTaskResolver, TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND, type KindResolver } from '../refs'
import type { TorqueApiClient } from '@tachyon/torque-client'
import { registerForgeRefs } from '../forge/refs'
import { registerTaskRefs } from './TaskDetailPane'
import { registerTesseractRefs } from './TesseractDetailPane'
import { createTesseractResolver } from './tesseractResolver'
import type { TesseractCache } from './tesseractDetail'

/**
 * Resolvers that talk to the real backend today: `torque.task`, through
 * the existing `/planner/torque-titles` batch endpoint. Forge kinds (G2) resolve through the ForgeClient. Every other kind stays
 * unresolved (chip + link, no preview) until its endpoint exists; see BACKEND_NEEDS.md.
 * Memoise the result before handing it to AutolinkProvider.
 */
export function createLiveResolvers(api: Pick<TorqueApiClient, 'plannerTaskTitles'>, tesseract?: TesseractCache): Record<string, KindResolver> {
  // Forge kinds (G2): registers pr/commit/repo (idempotent) and resolves through the ForgeClient (mock by default).
  const forge = registerForgeRefs().resolvers
  registerTaskRefs()
  const resolvers: Record<string, KindResolver> = { ...forge, 'torque.task': createTorqueTaskResolver((ids, signal) => api.plannerTaskTitles(ids, signal)) }
  if (tesseract) {
    // Read-only, through the console's Tesseract route; the same cache feeds the drawer.
    registerTesseractRefs()
    resolvers[TESSERACT_ITEM_KIND] = createTesseractResolver(TESSERACT_ITEM_KIND, tesseract)
    resolvers[TESSERACT_KEY_KIND] = createTesseractResolver(TESSERACT_KEY_KIND, tesseract)
  }
  return resolvers
}
