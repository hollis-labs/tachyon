import assert from "node:assert/strict"
import { after, test } from "node:test"
import {
  Activity,
  ClipboardList,
  GitBranch,
  Rocket,
  Server,
  Settings,
  Terminal,
  Users,
} from "lucide-react"
import { createServer } from "vite"

const server = await createServer({
  configFile: false,
  optimizeDeps: { noDiscovery: true },
  server: { middlewareMode: true, watch: null, hmr: false, ws: false },
  appType: "custom",
})
const { getNavIcon, isSupportedIcon, iconMap, SUPPORTED_ICON_NAMES } =
  await server.ssrLoadModule("/src/icons.ts")
after(() => server.close())

test("icons: real git-branch and clipboard-list icons are resolved correctly", () => {
  // scm-ops uses "git-branch"
  assert.equal(getNavIcon("git-branch"), GitBranch)
  assert.equal(iconMap["git-branch"], GitBranch)

  // work-ops uses "clipboard-list"
  assert.equal(getNavIcon("clipboard-list"), ClipboardList)
  assert.equal(iconMap["clipboard-list"], ClipboardList)
})

test("icons: existing and alias names resolve correctly", () => {
  assert.equal(getNavIcon("activity"), Activity)
  assert.equal(getNavIcon("git"), GitBranch)
  assert.equal(getNavIcon("clipboard"), ClipboardList)
  assert.equal(getNavIcon("play"), Rocket)
  assert.equal(getNavIcon("server"), Server)
  assert.equal(getNavIcon("settings"), Settings)
  assert.equal(getNavIcon("terminal"), Terminal)
  assert.equal(getNavIcon("users"), Users)
})

test("icons: isSupportedIcon identifies bounded vocabulary", () => {
  assert.equal(isSupportedIcon("git-branch"), true)
  assert.equal(isSupportedIcon("clipboard-list"), true)
  assert.equal(isSupportedIcon("users"), true)
  assert.equal(isSupportedIcon("unknown-icon"), false)
  assert.equal(isSupportedIcon(""), false)
  assert.ok(SUPPORTED_ICON_NAMES.includes("git-branch"))
  assert.ok(SUPPORTED_ICON_NAMES.includes("clipboard-list"))
})

test("icons: unknown or missing names fall back safely to Activity with console warning", (t) => {
  const warnings = []
  t.mock.method(console, "warn", (msg) => warnings.push(msg))

  assert.equal(getNavIcon(undefined), Activity)
  assert.equal(warnings.length, 0) // No warning for empty/undefined

  assert.equal(getNavIcon("non-existent-icon"), Activity)
  assert.equal(warnings.length, 1)
  assert.ok(warnings[0].includes("non-existent-icon"))

  // Inherited Object prototype properties must safely fall back to Activity
  assert.equal(getNavIcon("toString"), Activity)
  assert.equal(getNavIcon("constructor"), Activity)
  assert.equal(getNavIcon("__proto__"), Activity)
  assert.equal(getNavIcon("valueOf"), Activity)
  assert.equal(isSupportedIcon("toString"), false)
  assert.equal(isSupportedIcon("constructor"), false)
  assert.equal(isSupportedIcon("__proto__"), false)
  assert.equal(warnings.length, 5)
})
