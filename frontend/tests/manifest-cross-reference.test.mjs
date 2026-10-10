import assert from "node:assert/strict"
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { after, test } from "node:test"
import { fileURLToPath } from "node:url"
import { createServer } from "vite"

const server = await createServer({
  configFile: false,
  optimizeDeps: { noDiscovery: true },
  server: { middlewareMode: true, watch: null, hmr: false, ws: false },
  appType: "custom",
})
const { pageRegistry } = await server.ssrLoadModule("/src/pages/registry.ts")
const { isSupportedIcon, iconMap } = await server.ssrLoadModule("/src/icons.ts")
after(() => server.close())

// Load all plugin manifests from plugins/*/capabilities.json
const rootDir = fileURLToPath(new URL("../../", import.meta.url))
const pluginsDir = join(rootDir, "plugins")
const pluginDirs = readdirSync(pluginsDir, { withFileTypes: true })
  .filter((d) => d.isDirectory() && d.name !== "hello")
  .map((d) => d.name)

const manifests = {}
const allManifestRoutes = []
const allManifestIcons = []

for (const dir of pluginDirs) {
  const capPath = join(pluginsDir, dir, "capabilities.json")
  const raw = readFileSync(capPath, "utf-8")
  const manifest = JSON.parse(raw)
  manifests[dir] = manifest
  if (manifest.nav?.items) {
    for (const item of manifest.nav.items) {
      if (item.route) {
        allManifestRoutes.push(item.route)
      }
    }
  }
  if (manifest.nav?.groups) {
    for (const group of manifest.nav.groups) {
      if (group.icon) {
        allManifestIcons.push({ plugin: dir, group: group.id, icon: group.icon })
      }
    }
  }
}

// Host-owned routes that are not contributed by plugins
const HOST_OWNED_ROUTES = new Set(["/dashboard"])

test("manifest cross-reference: every plugin manifest nav route exists in pageRegistry", () => {
  assert.ok(allManifestRoutes.length > 0, "found manifest routes")
  for (const route of allManifestRoutes) {
    assert.ok(
      Object.hasOwn(pageRegistry, route),
      `Manifest route "${route}" missing from pageRegistry`,
    )
  }
})

test("manifest cross-reference: every page in pageRegistry maps to a plugin manifest or host route", () => {
  const registeredRoutes = Object.keys(pageRegistry)
  const manifestRouteSet = new Set(allManifestRoutes)

  for (const route of registeredRoutes) {
    const isManifestRoute = manifestRouteSet.has(route)
    const isHostRoute = HOST_OWNED_ROUTES.has(route)
    assert.ok(
      isManifestRoute || isHostRoute,
      `pageRegistry route "${route}" is neither a plugin manifest route nor a declared host route`,
    )
  }
})

test("manifest cross-reference: every plugin manifest group icon exists in the bounded icon set", () => {
  assert.ok(allManifestIcons.length > 0, "found manifest icons")
  for (const { plugin, group, icon } of allManifestIcons) {
    assert.ok(
      isSupportedIcon(icon),
      `Plugin "${plugin}" group "${group}" specifies icon "${icon}" not present in iconMap`,
    )
  }
})

test("manifest cross-reference: scm-ops uses git-branch and work-ops uses clipboard-list", () => {
  const scmManifest = manifests["scm-ops"]
  const scmGroup = scmManifest.nav?.groups?.find((g) => g.id === "source")
  assert.equal(scmGroup?.icon, "git-branch")
  assert.ok(isSupportedIcon("git-branch"))

  const workManifest = manifests["work-ops"]
  const workGroup = workManifest.nav?.groups?.find((g) => g.id === "work")
  assert.equal(workGroup?.icon, "clipboard-list")
  assert.ok(isSupportedIcon("clipboard-list"))
})

test("manifest cross-reference: every icon in iconMap maps to a valid React component", () => {
  for (const [name, Component] of Object.entries(iconMap)) {
    assert.ok(
      typeof Component === "function" || (typeof Component === "object" && Component !== null),
      `Icon component for "${name}" is not a valid component or forwardRef object`,
    )
  }
})
