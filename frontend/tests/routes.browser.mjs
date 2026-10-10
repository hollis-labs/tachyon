// Native acceptance over the production UI and isolated same-origin HTTP fixtures.
// Invoke after npm run build with PLAYWRIGHT_MODULE and CHROMIUM_EXECUTABLE.
import assert from "node:assert/strict"
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { createServer } from "node:http"
import { join, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)
const repo = fileURLToPath(new URL("../../", import.meta.url))
const output = resolve(process.env.ROUTE_PROOF_DIR || join(repo, ".scratch/captures"))
await mkdir(output, { recursive: true })
const manifests = {}
for (const id of ["work-ops", "config-ops", "agent-ops"])
  manifests[id] = JSON.parse(await readFile(join(repo, "plugins", id, "capabilities.json"), "utf8"))
let taskId = "Sparse ID / ?#% é🤖"
const task = {
  id: taskId,
  title: "Native routed task",
  status: "todo",
  priority: 3,
  project_id: "fixture",
  metadata: {},
}
let retiredIds = []
let missingVerb = false
let missingRead = false
let missingParent = false
let unknownPage = false
let discoveryFails = false
const calls = []
const nav = () => ({
  groups: Object.values(manifests).flatMap((manifest) => manifest.nav.groups),
  items: Object.entries(manifests)
    .filter(([id]) => !retiredIds.includes(id))
    .flatMap(([id, manifest]) =>
      manifest.nav.items
        .filter((item) => !(missingParent && item.route === "/work"))
        .map((item) => ({ ...item, plugin_id: id })),
    ),
})
const verbs = () =>
  Object.fromEntries(
    Object.entries(manifests)
      .filter(([id]) => !retiredIds.includes(id))
      .map(([_id, manifest]) => [
        manifest.modules[0],
        {
          modules: manifest.modules,
          verbs: Object.fromEntries(
            Object.entries(manifest.verbs).filter(
              ([verb]) =>
                !(missingVerb && verb === "work_list") && !(missingRead && verb === "work_read"),
            ),
          ),
        },
      ]),
  )
const server = createServer(async (req, res) => {
  try {
    if (req.url.startsWith("/api/")) {
      let body = ""
      for await (const chunk of req) body += chunk
      calls.push({ path: req.url, method: req.method, body: body ? JSON.parse(body) : undefined })
      res.setHeader("Content-Type", "application/json")
      let data
      if (req.url === "/api/nav") {
        if (discoveryFails) {
          res.writeHead(503)
          res.end("{}")
          return
        }
        data = nav()
        if (unknownPage)
          data.items.push({
            id: "unregistered",
            group: "work",
            label: "Unregistered",
            route: "/fixture",
            plugin_id: "work-ops",
          })
      } else if (req.url === "/api/verbs") data = verbs()
      else if (req.url === "/api/capabilities") data = { provider: "isolated-fixture" }
      else if (req.url === "/api/plugins/registry")
        data = {
          registry_version: 2,
          host_instance: "native-fixture",
          revision: 1,
          plugins: {},
          kinds: {},
          regions: {},
          contributions: {},
          refusals: [],
          retired_plugins: retiredIds.map((id) => ({
            id,
            name: id,
            state: "unloaded",
            reason: "native watchdog fixture",
            settings: {},
            retired_at: "2026-10-10T00:00:00Z",
          })),
        }
      else if (req.url === "/api/plugins/config-ops/restart") {
        retiredIds = retiredIds.filter((id) => id !== "config-ops")
        data = { id: "config-ops", status: "loaded" }
      } else if (req.url === "/api/health") data = { status: "ok" }
      else if (req.url === "/api/verb/work_list") {
        const filters = JSON.parse(body)
        const tasks = !filters.status || filters.status === task.status ? [task] : []
        data = {
          status: "ok",
          data: { tasks, total: tasks.length, has_more: false, next_offset: null },
        }
      } else if (req.url === "/api/verb/work_read") {
        assert.equal(JSON.parse(body).id, taskId)
        data = { status: "ok", data: task }
      } else if (req.url === "/api/verb/config_list") data = { status: "ok", data: [] }
      else if (req.url === "/api/verb/agent_list") data = { status: "ok", data: [] }
      else {
        res.writeHead(404)
        res.end(
          JSON.stringify({
            status: "error",
            error: { code: "fixture-not-implemented", message: req.url },
          }),
        )
        return
      }
      res.end(JSON.stringify(data))
      return
    }
    const pathname = new URL(req.url, "http://fixture").pathname
    const relative = pathname === "/sysop/" ? "index.html" : pathname.replace(/^\/sysop\//, "")
    if (!pathname.startsWith("/sysop/") || relative.includes("..")) {
      res.writeHead(404)
      res.end()
      return
    }
    res.setHeader(
      "Content-Type",
      relative.endsWith(".js")
        ? "text/javascript"
        : relative.endsWith(".css")
          ? "text/css"
          : "text/html",
    )
    res.end(await readFile(join(repo, "internal/webui/dist", relative)))
  } catch (error) {
    res.writeHead(500)
    res.end(String(error))
  }
})
await new Promise((done) => server.listen(0, "127.0.0.1", done))
const base = `http://127.0.0.1:${server.address().port}/sysop/`
let browser
const failures = []
const screenshots = []
const record = (message) => console.log(`PASS ${message}`)
try {
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_EXECUTABLE,
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
    env: { ...process.env },
  })
  const page = await browser.newPage({ viewport: { width: 1200, height: 850 } })
  page.on("pageerror", (error) => failures.push(error.message))
  async function capture(name) {
    await page.screenshot({ path: join(output, `${name}.png`), fullPage: true })
    screenshots.push(name)
  }
  async function ready(path, heading) {
    await page.goto(`${base}#${path}`)
    // Hash navigation does not fetch a new host snapshot. A changed fixture
    // represents a new discovery/reload scenario, not a push to the live client.
    await page.reload()
    await page.getByRole("heading", { name: heading, exact: true, includeHidden: true }).waitFor()
  }
  await ready("/work/board", "Work Board")
  await page.reload()
  await page.getByRole("heading", { name: "Work Board", exact: true }).waitFor()
  assert.equal(new URL(page.url()).pathname, "/sysop/")
  assert.equal(
    await page.getByRole("button", { name: "Board", exact: true }).getAttribute("aria-current"),
    "page",
  )
  await capture("01-deep-link-reload")
  record("static subview deep link and native reload preserve /sysop/ and active rail")
  await page.getByRole("button", { name: "Tasks", exact: true }).click()
  await page.getByRole("heading", { name: "Work Tracking", exact: true }).waitFor()
  await page.getByRole("button", { name: /Native routed task/ }).click()
  await page.getByRole("dialog").waitFor()
  assert.equal(decodeURIComponent(new URL(page.url()).hash.slice("#/work/".length)), taskId)
  assert.deepEqual(await page.locator('nav[aria-label="Breadcrumbs"] li').allTextContents(), [
    "Work",
    "›Tasks",
    "›Task details",
  ])
  await page.reload()
  await page.getByRole("dialog").waitFor()
  await capture("02-encoded-selection-reload")
  record("arbitrary sparse task ID round trips and real detail selection survives reload")
  await page.getByRole("dialog").locator('button[data-slot="dialog-close"]').click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await page.goBack()
  await page.getByRole("dialog").waitFor()
  assert.equal(await page.getByRole("dialog").getByText(taskId, { exact: true }).count(), 1)
  await page.goForward()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await page.goBack()
  await page.getByRole("dialog").waitFor()
  await page.goBack()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await page.goBack()
  await page.getByRole("heading", { name: "Work Board", exact: true }).waitFor()
  await page.goForward()
  await page.getByRole("heading", { name: "Work Tracking", exact: true }).waitFor()
  record("native back/forward changes committed page and detail selection, no stale useState")
  assert.deepEqual(await page.locator('nav[aria-label="Breadcrumbs"] li').allTextContents(), [
    "Work",
    "›Tasks",
  ])
  for (const path of ["/unknown-hidden", "/work/", "/work/%ZZ", "/work/board/extra"]) {
    await ready(path, "Page not available")
    assert.equal(
      await page
        .getByRole("button", { name: "Dashboard", exact: true })
        .getAttribute("aria-current"),
      null,
    )
  }
  record("unknown, malformed, trailing and extra subview routes show explicit not available")
  // A literal sibling still opens the board; an encoded opaque ID opens detail.
  taskId = "board"
  task.id = taskId
  await ready("/work/%62oard", "Work Tracking")
  await page.getByRole("dialog").getByText("board", { exact: true }).waitFor()
  await ready("/work/board", "Work Board")
  taskId = "%2F"
  task.id = taskId
  await ready("/work/%252F", "Work Tracking")
  await page.getByRole("dialog").getByText("%2F", { exact: true }).waitFor()
  record("static sibling precedence and once-decoded encoded ID data")
  missingRead = true
  await ready("/work/%252F", "Page not available")
  assert.equal(
    await page.locator("[data-route-reason]").getAttribute("data-route-reason"),
    "missing-verb",
  )
  missingRead = false
  missingParent = true
  await ready("/work/%252F", "Page not available")
  assert.equal(
    await page.locator("[data-route-reason]").getAttribute("data-route-reason"),
    "unknown-route",
  )
  missingParent = false
  record("detail requires work_read and currently admitted parent")
  missingVerb = true
  await ready("/work", "Page not available")
  assert.equal(
    await page.locator("[data-route-reason]").getAttribute("data-route-reason"),
    "missing-verb",
  )
  missingVerb = false
  unknownPage = true
  await ready("/fixture", "Page not available")
  assert.equal(
    await page.locator("[data-route-reason]").getAttribute("data-route-reason"),
    "unregistered-page",
  )
  unknownPage = false
  discoveryFails = true
  await ready("/not-discovered", "Page not available")
  assert.equal(
    await page.locator("[data-route-reason]").getAttribute("data-route-reason"),
    "discovery-unavailable",
  )
  discoveryFails = false
  record("missing verb, unregistered declared page and failed discovery use distinct evidence")
  retiredIds = ["work-ops"]
  await ready("/work/%252F", "Page not available")
  assert.equal(
    await page.locator("[data-route-reason]").getAttribute("data-route-reason"),
    "plugin-retired",
  )
  await page.getByText(/host reports plugin work-ops as retired/).waitFor()
  await capture("03-retired-direct-link")
  record("retired plugin direct link uses actual tombstone plus compiled source ownership")
  retiredIds = ["config-ops"]
  await ready("/plugin-recovery", "Plugin recovery")
  await page.getByRole("button", { name: "Restart plugin", exact: true }).click()
  await page.getByRole("heading", { name: "Settings / Plugins", exact: true }).waitFor()
  await page.waitForFunction(
    () => document.activeElement?.getAttribute("aria-label") === "Configuration",
  )
  assert.equal(new URL(page.url()).hash, "#/settings")
  await capture("04-recovery-focus")
  record(
    "explicit recovery refresh replaces retired recovery route and restores focus to Settings rail",
  )
  assert.equal(calls.filter((call) => call.path === "/api/plugins/config-ops/restart").length, 1)
  assert.ok(calls.filter((call) => call.path === "/api/verb/work_read").length >= 2)
  assert.deepEqual(failures, [])
  await writeFile(
    join(output, "receipt.json"),
    JSON.stringify(
      {
        browser: browser.version(),
        nativeAuthor: "task-tachyon-CW-20261010-0116",
        fixture: "production UI, actual source manifests, isolated HTTP provider fixtures",
        base,
        screenshots,
        calls,
        failures,
      },
      null,
      2,
    ),
  )
  record("same-origin clients and native UI scenarios; no page errors")
} finally {
  await browser?.close()
  await new Promise((done) => server.close(done))
}
