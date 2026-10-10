import assert from "node:assert/strict"
import { after, test } from "node:test"
import { createServer } from "vite"

const server = await createServer({
  configFile: false,
  optimizeDeps: { noDiscovery: true },
  server: { middlewareMode: true, watch: null, hmr: false, ws: false },
  appType: "custom",
})
const { visibleNavigation } = await server.ssrLoadModule("/src/api/navigation.ts")
after(() => server.close())

test("visibleNavigation: empty or undefined navigation returns empty array", () => {
  assert.deepEqual(
    visibleNavigation(
      {},
      () => true,
      () => true,
    ),
    [],
  )
  assert.deepEqual(
    visibleNavigation(
      { groups: [], items: [] },
      () => true,
      () => true,
    ),
    [],
  )
  assert.deepEqual(
    visibleNavigation(
      { groups: [{ id: "g1", label: "G1" }] },
      () => true,
      () => true,
    ),
    [],
  )
})

test("visibleNavigation: filters out items without a registered page", () => {
  const nav = {
    groups: [{ id: "group-a", label: "Group A", priority: 100 }],
    items: [
      { id: "item-1", label: "Item 1", group: "group-a", route: "/valid" },
      { id: "item-2", label: "Item 2", group: "group-a", route: "/unregistered" },
    ],
  }
  const result = visibleNavigation(
    nav,
    () => true,
    (route) => route === "/valid",
  )
  assert.equal(result.length, 1)
  assert.equal(result[0].id, "item-1")
})

test("visibleNavigation: verb gating checks requires_verb capability", () => {
  const nav = {
    groups: [{ id: "group-a", label: "Group A", priority: 100 }],
    items: [
      { id: "open", label: "Open Item", group: "group-a", route: "/open" },
      {
        id: "gated-allowed",
        label: "Gated Allowed",
        group: "group-a",
        route: "/gated-1",
        requires_verb: "agent_list",
      },
      {
        id: "gated-denied",
        label: "Gated Denied",
        group: "group-a",
        route: "/gated-2",
        requires_verb: "agent_delete",
      },
    ],
  }
  const result = visibleNavigation(
    nav,
    (verb) => verb === "agent_list",
    () => true,
  )
  assert.equal(result.length, 2)
  assert.deepEqual(
    result.map((i) => i.id),
    ["gated-allowed", "open"],
  )
})

test("visibleNavigation: sorts deterministically by group priority, group ID, item priority, item ID", () => {
  const nav = {
    groups: [
      { id: "zeta", label: "Zeta", priority: 200 },
      { id: "alpha", label: "Alpha", priority: 100 },
      { id: "beta", label: "Beta", priority: 100 },
    ],
    items: [
      { id: "z-item", label: "Z Item", group: "zeta", route: "/z", priority: 10 },
      { id: "b-item-2", label: "B2", group: "beta", route: "/b2", priority: 50 },
      { id: "b-item-1", label: "B1", group: "beta", route: "/b1", priority: 10 },
      { id: "a-item-b", label: "Ab", group: "alpha", route: "/ab", priority: 20 },
      { id: "a-item-a", label: "Aa", group: "alpha", route: "/aa", priority: 20 },
    ],
  }
  const result = visibleNavigation(
    nav,
    () => true,
    () => true,
  )
  assert.deepEqual(
    result.map((i) => i.id),
    ["a-item-a", "a-item-b", "b-item-1", "b-item-2", "z-item"],
  )
})

test("visibleNavigation: defaults missing or zero priority to 1000", () => {
  const nav = {
    groups: [
      { id: "custom", label: "Custom", priority: 500 },
      { id: "default-pri", label: "Default Pri" },
    ],
    items: [
      { id: "def-item", label: "Default Item", group: "default-pri", route: "/def" },
      { id: "cust-item", label: "Custom Item", group: "custom", route: "/cust" },
    ],
  }
  const result = visibleNavigation(
    nav,
    () => true,
    () => true,
  )
  assert.deepEqual(
    result.map((i) => i.id),
    ["cust-item", "def-item"],
  )
})

test("visibleNavigation: preserves host-added plugin_id and all metadata fields", () => {
  const nav = {
    groups: [{ id: "agents", label: "Agents", priority: 100, plugin_id: "agent-ops" }],
    items: [
      {
        id: "agent_list",
        label: "Agent Ops",
        group: "agents",
        route: "/agents",
        plugin_id: "agent-ops",
        requires_verb: "agent_list",
        priority: 100,
      },
    ],
  }
  const result = visibleNavigation(
    nav,
    () => true,
    () => true,
  )
  assert.equal(result.length, 1)
  assert.equal(result[0].plugin_id, "agent-ops")
  assert.equal(result[0].route, "/agents")
  assert.equal(result[0].group, "agents")
})
