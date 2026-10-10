import assert from "node:assert/strict"
import { after, test } from "node:test"
import { createServer } from "vite"

const server = await createServer({
  configFile: false,
  optimizeDeps: { noDiscovery: true },
  server: { middlewareMode: true, watch: null, hmr: false, ws: false },
  appType: "custom",
})
const { parseLocation, validPattern, matchRoute, routePath, legacyRouteCatalog, resolveRoute } =
  await server.ssrLoadModule("/src/routing/routes.ts")
const { createHashStore } = await server.ssrLoadModule("/src/routing/hash-route.tsx")
after(() => server.close())

const catalog = {
  groups: [{ id: "__proto__", label: "Work" }],
  items: [{ id: "constructor", label: "Tasks", group: "__proto__", page: "tasks" }],
  pages: [
    {
      id: "tasks",
      route: "/work",
      title: "Tasks",
      view: "work",
      owner: "work-ops",
      requiresVerbs: ["work_list"],
    },
    {
      id: "detail",
      route: "/work/hidden",
      title: "Details",
      view: "work-detail",
      owner: "work-ops",
      hidden: true,
    },
    { id: "board", route: "/work/board", title: "Board", view: "work-board", owner: "work-ops" },
  ],
  subviews: [{ id: "sparse.900", label: "Board", parent: "constructor", page: "board" }],
}
const inputs = {
  hasVerb: () => true,
  hasView: () => true,
  retired: [],
  compiledOwner: () => undefined,
}
const resolve = (path, overrides = {}, data = catalog) =>
  resolveRoute(parseLocation(path), data, { ...inputs, ...overrides })

test("static identity, query ownership and invalid paths do not silently normalize", () => {
  assert.equal(parseLocation("").path, "/agents")
  assert.equal(parseLocation("#/work?task=A%2FB&tab=notes").query.get("task"), "A/B")
  for (const path of [
    "/work/",
    "/work//board",
    "/work/%",
    "/work/%00",
    "/work/%2e%2e",
    "//work",
    "work",
    "/work\\board",
    "/work#x",
  ])
    assert.equal(parseLocation(path).valid, false, path)
  for (const path of [
    "/",
    "/Work",
    "/work/",
    "/work/*",
    "/work/:ID",
    "/work/:id/:id",
    "/work?x=1",
    `/${"a".repeat(128)}`,
  ])
    assert.equal(validPattern(path), false, path)
  assert.equal(validPattern("/work/board"), true)
  assert.equal(matchRoute("/work", "/work/missing"), null)
  assert.equal(resolve("/work/").reason, "invalid-route")
  assert.equal(resolve("/unknown").reason, "unknown-route")
})

test("page-owned query IDs round trip safely; colon admission stays held", () => {
  for (const id of ["CW-20261010-0116", "A/B ?#%", "é🤖", "constructor", "__proto__", " board "]) {
    const query = new URLSearchParams({ task: id })
    const path = `/work?${query}`
    assert.equal(parseLocation(path).query.get("task"), id)
    assert.equal(resolve(path).page.id, "tasks")
  }
  assert.equal(parseLocation("/work?task=%252F").query.get("task"), "%2F")
  assert.equal(validPattern("/work/:id"), false)
  assert.equal(matchRoute("/work/:id", "/work/123"), null)
  assert.equal(resolve("/work/board").page.id, "board")
  assert.equal(resolve("/work/123/extra").reason, "unknown-route")
  assert.throws(() => routePath("/work/:id"), /pattern/)
})

test("hidden declared pages resolve; visibility is never registration evidence", () => {
  const result = resolve("/work/hidden")
  assert.equal(result.reason, undefined)
  assert.equal(result.page.hidden, true)
  assert.equal(result.page.id, "detail")
  assert.equal(resolve("/work/hidden", { hasView: () => false }).reason, "unregistered-page")
  assert.equal(resolve("/secret", {}, { ...catalog, pages: [] }).reason, "unknown-route")
})

test("unavailability reasons require actual facts, including retirement on direct reload", () => {
  assert.equal(resolve("/work", { hasVerb: () => false }).reason, "missing-verb")
  assert.equal(resolve("/work", { hasView: () => false }).reason, "unregistered-page")
  const result = resolve("/work", { retired: [{ id: "work-ops", reason: "watchdog" }] })
  assert.equal(result.reason, "plugin-retired")
  assert.match(result.detail, /watchdog/)
  assert.equal(resolve("/work", { retired: [{ id: "other" }] }).reason, undefined)
  assert.equal(
    resolve(
      "/work",
      { compiledOwner: () => "work-ops", retired: [{ id: "work-ops" }] },
      { ...catalog, pages: [] },
    ).reason,
    "plugin-retired",
  )
  assert.equal(resolve("/unknown", { retired: [{ id: "work-ops" }] }).reason, "unknown-route")
  assert.equal(
    resolve("/unknown", { discoveryError: "Host fetch failed" }).reason,
    "discovery-unavailable",
  )
})

test("breadcrumbs use group, owning item and declared subview; active item follows subview", () => {
  const result = resolve("/work/board")
  assert.deepEqual(
    result.breadcrumbs.map((crumb) => crumb.label),
    ["Work", "Tasks", "Board"],
  )
  assert.equal(result.breadcrumbs[1].route, "/work")
  assert.equal(result.itemId, "constructor")
  const gated = { ...catalog, subviews: [{ ...catalog.subviews[0], requiresVerbs: ["work_read"] }] }
  assert.equal(
    resolve("/work/board", { hasVerb: (verb) => verb !== "work_read" }, gated).reason,
    "missing-verb",
  )
})

test("legacy adapter admits declarations independently of compiled views and respects host routes", () => {
  const nav = {
    groups: [],
    items: [
      {
        id: "10",
        label: "Tasks",
        route: "/work",
        group: "999",
        plugin_id: "work-ops",
        requires_verb: "work_list",
      },
      { id: "20", label: "Dashboard", route: "/dashboard", group: "g", plugin_id: "rogue" },
      { id: "30", label: "Recovery", route: "/plugin-recovery", group: "g", plugin_id: "rogue" },
      { id: "40", label: "Settings", route: "/settings/child", group: "g", plugin_id: "rogue" },
      { id: "50", label: "Settings", route: "/settings", group: "g", plugin_id: "config-ops" },
    ],
  }
  const result = legacyRouteCatalog(nav)
  assert.deepEqual(
    result.pages.map((page) => page.id),
    ["10", "50"],
  )
  assert.equal(resolve("/work", { hasView: () => false }, result).reason, "unregistered-page")
})

test("history store reads committed hash, preserves /sysop/ and publishes push/replace/back", () => {
  const events = new EventTarget()
  const browser = {
    location: { pathname: "/sysop/", search: "?host=one", hash: "#/work" },
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
  }
  const history = ["#/work"]
  let index = 0
  browser.history = {
    pushState(_state, _title, url) {
      assert.ok(url.startsWith("/sysop/?host=one#"))
      history.splice(++index)
      history[index] = browser.location.hash = url.slice(url.indexOf("#"))
    },
    replaceState(_state, _title, url) {
      history[index] = browser.location.hash = url.slice(url.indexOf("#"))
    },
  }
  const store = createHashStore(browser)
  const committed = []
  const unsubscribe = store.subscribe(() => committed.push(store.snapshot()))
  store.navigate("/work?task=a%2Fb")
  store.navigate("/work/board")
  browser.location.hash = history[--index]
  events.dispatchEvent(new Event("popstate"))
  assert.equal(parseLocation(store.snapshot()).query.get("task"), "a/b")
  browser.location.hash = history[++index]
  events.dispatchEvent(new Event("hashchange"))
  assert.equal(parseLocation(store.snapshot()).path, "/work/board")
  store.navigate("/dashboard", true)
  assert.deepEqual(committed, [
    "#/work?task=a%2Fb",
    "#/work/board",
    "#/work?task=a%2Fb",
    "#/work/board",
    "#/dashboard",
  ])
  assert.equal(history.length, 3)
  assert.throws(() => store.navigate("https://elsewhere.test"), /Invalid/)
  unsubscribe()
  store.navigate("/agents")
  assert.equal(committed.length, 5)
})
