import assert from "node:assert/strict"
import { after, test } from "node:test"
import { createServer } from "vite"

// Load the authored TypeScript through the existing Vite toolchain. No host,
// provider, model CLI, listening socket, or browser is needed for these checks.
const server = await createServer({
  configFile: false,
  server: { middlewareMode: true, watch: null },
  appType: "custom",
})
const { workApi, workPageInfo } = await server.ssrLoadModule("/src/api/work.ts")
after(() => server.close())

const task = { id: "one", status: "doing", project_id: "project-one" }
const page = { tasks: [task], total: 3, has_more: true, next_offset: 1 }

test("list forwards typed server filters and reads only the requested page", async (t) => {
  const calls = []
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, method: options.method, body: JSON.parse(options.body) })
    return new Response(JSON.stringify({ status: "ok", data: page }))
  })
  const filters = {
    status: "doing",
    project_id: "project-one",
    tags: "team,frontend",
    limit: 50,
    offset: 0,
  }
  assert.deepEqual(await workApi.list(filters), { status: "ok", data: page })
  assert.deepEqual(calls, [{ url: "/api/verb/work_list", method: "POST", body: filters }])
  await workApi.list({ ...filters, offset: page.next_offset })
  assert.equal(calls.length, 2)
  assert.equal(calls[1].body.offset, 1)
})

test("search stays on work_search with the existing capped-result envelope", async (t) => {
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url, "/api/verb/work_search")
    assert.deepEqual(JSON.parse(options.body), { query: "handoff" })
    return new Response(JSON.stringify({ status: "ok", data: { ...page, next_offset: null } }))
  })
  assert.deepEqual(await workApi.search("handoff"), {
    status: "ok",
    data: { ...page, next_offset: null },
  })
})

test("list preserves approval and error envelopes", async (t) => {
  for (const envelope of [
    { status: "ask", ask: { prompt: "Approve read?" } },
    { status: "error", error: { code: "unavailable", message: "Try later" } },
  ]) {
    t.mock.method(
      globalThis,
      "fetch",
      async () => new Response(JSON.stringify(envelope), { status: 503 }),
    )
    assert.deepEqual(await workApi.list(), envelope)
  }
})

test("continuations and remaining counts come from provider offsets", () => {
  assert.deepEqual(workPageInfo(page, 0), { total: 3, hasMore: true, next: 1, more: 2 })
  assert.deepEqual(workPageInfo({ ...page, total: 1, has_more: false, next_offset: null }, 0), {
    total: 1,
    hasMore: false,
    next: undefined,
    more: undefined,
  })
  // Loaded IDs can overlap as offset pages drift; totals are not derived from unique rows.
  assert.equal(workPageInfo({ ...page, total: 5, next_offset: 3 }, 2).more, 2)
})

test("missing or inconsistent totals remain unknown while valid continuation survives", () => {
  for (const total of [undefined, -1, 0, 1, 1.5, Number.NaN]) {
    const info = workPageInfo({ ...page, total }, 0)
    assert.equal(info.total, undefined)
    assert.equal(info.more, undefined)
    assert.equal(info.hasMore, true)
    assert.equal(info.next, 1)
  }
  assert.equal(workPageInfo({ ...page, total: 5, has_more: false }, 0).total, undefined)
})

test("invalid continuation fails instead of looping or inventing a remaining count", () => {
  for (const next_offset of [undefined, null, -1, 0, 1.5])
    assert.throws(() => workPageInfo({ ...page, next_offset }, 0), /continuation is invalid/)
  assert.throws(() => workPageInfo({ ...page, next_offset: 1 }, 1), /continuation is invalid/)
  assert.throws(() => workPageInfo({ ...page, tasks: undefined }, 0), /incomplete/)
  assert.throws(() => workPageInfo({ ...page, has_more: undefined }, 0), /incomplete/)
})

test("ignored server filters cannot present an unrelated page as matching tasks", () => {
  assert.throws(() => workPageInfo(page, 0, { status: "todo" }), /status filter/)
  assert.throws(() => workPageInfo(page, 0, { project_id: "other" }), /project filter/)
  assert.equal(workPageInfo(page, 0, { status: "doing", project_id: "project-one" }).total, 3)
})

test("total below the loaded page end is unknown even when it exceeds the page length", () => {
  const info = workPageInfo(
    {
      ...page,
      total: 7,
      next_offset: 6,
      tasks: [task, { ...task, id: "two" }, { ...task, id: "three" }],
    },
    5,
  )
  assert.equal(info.total, undefined)
  assert.equal(info.more, undefined)
})

test("remaining count uses provider next_offset even when it differs from loaded end", () => {
  const info = workPageInfo({ ...page, total: 12, next_offset: 8 }, 2)
  assert.equal(info.total, 12)
  assert.equal(info.next, 8)
  assert.equal(info.more, 4)
})
