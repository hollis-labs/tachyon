import agent from "../../../plugins/agent-ops/capabilities.json"
import config from "../../../plugins/config-ops/capabilities.json"
import launch from "../../../plugins/launch-ops/capabilities.json"
import observe from "../../../plugins/observe-ops/capabilities.json"
import scm from "../../../plugins/scm-ops/capabilities.json"
import service from "../../../plugins/service-ops/capabilities.json"
import session from "../../../plugins/session-ops/capabilities.json"
import work from "../../../plugins/work-ops/capabilities.json"

// Only retirement attribution, never route admission. A retired plugin is no
// longer in /api/nav; the actual compiled source manifests establish ownership
// for its known static paths on a direct reload. Tombstones establish retirement.
const owners = new Map<string, string>()
for (const [owner, manifest] of [
  ["agent-ops", agent],
  ["config-ops", config],
  ["launch-ops", launch],
  ["observe-ops", observe],
  ["scm-ops", scm],
  ["service-ops", service],
  ["session-ops", session],
  ["work-ops", work],
] as const) {
  for (const item of manifest.nav.items) owners.set(item.route, owner)
}
export const compiledOwner = (path: string) => owners.get(path)
