import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { after, test } from "node:test"
import { createPluginRegistry } from "@hollis-labs/plugin-registry"
import { createServer } from "vite"

const server = await createServer({
  configFile: false,
  optimizeDeps: { noDiscovery: true },
  server: { middlewareMode: true, watch: null, hmr: false, ws: false },
  appType: "custom",
})
const { navKinds, navRegions } = await server.ssrLoadModule("/src/plugins/nav-descriptors.ts")
after(() => server.close())
const raw = await readFile("../internal/plugins/testdata/nav-schema1-registry.json", "utf8")
const fixture = JSON.parse(raw)

test("DEC085: published Go descriptors equal source-only TS descriptors", () => {
  assert.deepEqual(navKinds, fixture.kinds)
  assert.deepEqual(navRegions, fixture.regions)
})

test("actual host schema-1 projection admits under published TS registry", async () => {
  const registry = createPluginRegistry({
    kinds: navKinds,
    regions: navRegions,
    stylesheets: false,
  })
  try {
    const result = await registry.sync(raw)
    assert.equal(result.accepted, true)
    const snapshot = registry.snapshot()
    assert.equal(snapshot.contributions.length, 38)
    assert.equal(snapshot.refusals.length, 0)
    assert.equal(snapshot.contributions.filter((c) => c.kind === "page").length, 15)
    assert.equal(fixture.nav_projection.status, "ok")
  } finally {
    await registry.clear()
  }
})

test("an unsupported optional descriptor produces named TS admission refusal", async () => {
  const kinds = { ...navKinds, page: { ...navKinds.page, schema_version: 2 } }
  const registry = createPluginRegistry({ kinds, regions: navRegions, stylesheets: false })
  try {
    const result = await registry.sync(raw)
    assert.equal(result.accepted, true)
    const snapshot = registry.snapshot()
    assert.equal(snapshot.refusals.filter((r) => r.reason === "unsupported-schema").length, 15)
    assert.equal(snapshot.contributions.filter((c) => c.kind === "page").length, 0)
  } finally {
    await registry.clear()
  }
})
