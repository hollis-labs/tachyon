/**
 * A local fake of the Tether endpoints the binary's chat bridge reads (internal/chat/chatbridge), for the
 * TACHYON_E2E_CHAT=backend run only. It listens on loopback, holds one agent on one channel in memory,
 * and never forwards anything: the run's binary points tachyon.upstreams.tether.url here and nowhere else.
 *
 *   GET  /chat/agents                     the chat roster {agents:[...]}
 *   GET  /channels/:name/messages?since=  publications after a cursor {name,address,messages,next_since}
 *   GET  /routing/capabilities?session=   reply routing for a session
 *   POST /messages/:id/reply              a reply to a publication; the receipt
 *   GET  /messages/:id/delivery           a reply's delivery, always delivered
 *   POST /messages                        mail (Agent Ops nudges); a payload containing REFUSE answers 503
 *
 * For the builder backend run (TACHYON_E2E_BUILDER=backend), the reads and team operations the binary makes:
 *   GET  /catalog/files[?path=]           the catalog listing {files:[...]}, or one file {content}
 *   GET  /catalog/history                 no catalog plans yet {plans:[]}
 *   GET  /registry/projects               the registry {projects:[...]}
 *   GET  /sessions                        no running sessions {sessions:[]}
 *   POST /teams/form                      a formation receipt {run, members}
 *   GET  /teams/roster?run_id=            the run's roster {runs:[{run, roster_version, members}]}
 *   POST /teams/{assign,address,member_add,member_remove,cancel,dissolve}
 *                                          a body containing REFUSE answers 503, for the refusal path
 * Team shapes follow mesh/core teams (TeamRun, Member) as internal/provideractions parses them; the run seeded
 * by e2e/builder-seed.sql (TEAM_RUN) is "working", as Tether reports a formed run.
 *
 * Shapes follow mesh/core tetherclient (ChannelMessage, RoutingCapabilitiesResponse, ReplyReceipt,
 * ReplyDelivery). Environment: E2E_TETHER_ADDR host:port to listen on.
 */
import { createHash } from "node:crypto"
import { createServer, type IncomingMessage, type ServerResponse } from "node:http"
import { pathToFileURL } from "node:url"

export const CHANNEL = "chat-e2e"
export const SESSION_ID = "sess-e2e-1"
export const LAUNCH_ID = "L_e2e"
const T0 = Date.parse("2026-11-01T09:00:00Z")

const agents = [
  {
    launch_id: LAUNCH_ID,
    agent_id: LAUNCH_ID,
    display_name: "Reviewer",
    channel: CHANNEL,
    session: {
      id: SESSION_ID,
      state: "running",
      at_turn_boundary: true,
      last_activity_at: new Date(T0 + 60_000).toISOString(),
    },
  },
]

/** A routed turn output: the session publishes on the channel with its turn identity. */
function publication(seq: number, kind: string, text: string, at: number) {
  return {
    id: `pub-e2e-${seq}`,
    kind: "notice",
    channel: CHANNEL,
    from: `msg://session/local/${SESSION_ID}`,
    to: `msg://service/local/channel/${CHANNEL}`,
    thread_id: SESSION_ID,
    payload: { text },
    metadata: {
      kind,
      session_id: SESSION_ID,
      turn_id: `turn-e2e-${seq}`,
      logical_agent_id: LAUNCH_ID,
      launch_id: LAUNCH_ID,
    },
    created_at: new Date(at).toISOString(),
    delivered_at: null,
    consumed_at: null,
    seq,
  }
}

/** The run e2e/builder-seed.sql records as formed: the same ids, so a roster refresh matches its receipt. */
export const TEAM_RUN = {
  id: "run-e2e-1",
  team_id: "tachyon-e2e00000-0000-4000-8000-0000000000d1-e2e00000-0000-4000-8000-0000000000f1",
  team_version: 1,
  channel: "team/run-e2e-1",
  status: "working",
}
const teamMembers = [
  { id: "mem-e2e-lead", slot: "lead", actor: "agt_e2e_lead", status: "active", kind: "agent" },
  {
    id: "mem-e2e-reviewer",
    slot: "reviewer",
    actor: "agt_e2e_reviewer",
    status: "active",
    kind: "agent",
  },
]
let rosterVersion = 1

/** The catalog files the binary's catalog index reads: names follow e2e/builder-fixtures. */
const catalogFiles: Record<string, string> = {
  "agents/builder-reviewer.yaml": [
    "id: builder-reviewer",
    "name: Builder Reviewer",
    "provider: claude",
    "model: claude-sonnet-5-5",
    "skills: [review-etiquette]",
  ].join("\n"),
  "agents/release-captain.yaml": [
    "id: release-captain",
    "name: Release Captain",
    "provider: claude",
    "model: claude-opus-5-5",
  ].join("\n"),
  "launches/builder-reviewer-tachyon.yaml": [
    "id: builder-reviewer-tachyon",
    "agent: builder-reviewer",
    "project: tachyon",
    "worker: hub",
  ].join("\n"),
  "launches/release-captain-tachyon.yaml": [
    "id: release-captain-tachyon",
    "agent: release-captain",
    "project: tachyon",
    "worker: hub",
  ].join("\n"),
  "projects/tachyon.yaml": ["id: tachyon", "name: Tachyon"].join("\n"),
  "projects/example-app.yaml": ["id: example-app", "name: Example App"].join("\n"),
  "skills/review-etiquette.md": [
    "---",
    "name: review-etiquette",
    "description: How to word review comments.",
    "---",
    "Be specific and kind.",
  ].join("\n"),
  "teams-policy.yaml": [
    "version: 1",
    "launches:",
    "  - launch: builder-reviewer-tachyon",
    "    runtime: claude",
    "    definition:",
    "      id: builder-reviewer",
    '      revision: "1"',
    `      digest: sha256:${"a".repeat(64)}`,
  ].join("\n"),
}
const registryProjects = [
  {
    urn: "urn:tether:project:tachyon",
    kind: "project",
    display_name: "Tachyon",
    description: "The operator console.",
    status: "active",
    tags: ["app", "go"],
    props: { id: "tachyon" },
  },
  {
    urn: "urn:tether:project:example-app",
    kind: "project",
    display_name: "Example App",
    description: "Agent runtime and workspaces.",
    status: "active",
    tags: ["runtime"],
    props: { id: "example-app" },
  },
]
const teamVerbs = new Set([
  "assign",
  "address",
  "member_add",
  "member_remove",
  "cancel",
  "dissolve",
])

const messages = [
  publication(1, "question", "Shall I start on the chat port?", T0),
  publication(2, "final", "The planner surface is ready for review.", T0 + 60_000),
]
const replies = new Map<string, { parent: string; at: string }>()

function send(res: ServerResponse, status: number, body: unknown) {
  res.writeHead(status, { "Content-Type": "application/json" })
  res.end(JSON.stringify(body))
}

async function text(req: IncomingMessage): Promise<string> {
  let raw = ""
  for await (const chunk of req) raw += chunk
  return raw
}

async function body(req: IncomingMessage): Promise<Record<string, unknown>> {
  const raw = await text(req)
  return raw ? JSON.parse(raw) : {}
}

/** The builder run's catalog, registry and team endpoints; false when the path is not one of them. */
async function builder(req: IncomingMessage, res: ServerResponse, url: URL): Promise<boolean> {
  const path = url.pathname
  if (req.method === "GET" && path === "/catalog/files") {
    const one = url.searchParams.get("path")
    if (one === null) {
      const files = Object.entries(catalogFiles).map(([p, content]) => ({
        path: p,
        layer: "system",
        sha256: createHash("sha256").update(content).digest("hex"),
      }))
      send(res, 200, { files })
    } else if (one in catalogFiles) send(res, 200, { path: one, content: catalogFiles[one] })
    else send(res, 404, { error: "no such catalog file" })
    return true
  }
  if (req.method === "GET" && path === "/catalog/history") {
    send(res, 200, { plans: [] })
    return true
  }
  if (req.method === "GET" && path === "/registry/projects") {
    send(res, 200, { projects: registryProjects })
    return true
  }
  if (req.method === "GET" && path === "/sessions") {
    send(res, 200, { sessions: [], next_cursor: null })
    return true
  }
  if (!path.startsWith("/teams/")) return false
  // Tether's team routes require the protocol header provideractions sends.
  if (req.headers["tether-protocol"] !== "1") {
    send(res, 400, { error: "Tether-Protocol: 1 required" })
    return true
  }
  if (req.method === "GET" && path === "/teams/roster") {
    if (url.searchParams.get("run_id") !== TEAM_RUN.id) send(res, 200, { runs: [] })
    else
      send(res, 200, {
        runs: [{ run: TEAM_RUN, roster_version: rosterVersion, members: teamMembers }],
      })
    return true
  }
  if (req.method !== "POST") return false
  const raw = await text(req)
  if (!req.headers["idempotency-key"]) {
    send(res, 400, { error: "Idempotency-Key required" })
    return true
  }
  if (raw.includes("REFUSE")) {
    send(res, 503, { error: "team host unavailable" })
    return true
  }
  const input = raw ? JSON.parse(raw) : {}
  if (path === "/teams/form") {
    const team = input.team ?? {}
    send(res, 200, {
      run: {
        ...TEAM_RUN,
        id: `run-e2e-${Date.now()}`,
        team_id: team.id,
        team_version: team.version,
      },
      members: [],
    })
    return true
  }
  const verb = path.slice("/teams/".length)
  if (!teamVerbs.has(verb)) return false
  if (input.run_id !== TEAM_RUN.id) {
    send(res, 404, { error: "no such run" })
    return true
  }
  if (verb === "address" || verb === "assign") {
    const slot = typeof input.address === "string" ? input.address.replace(/^@/, "") : ""
    const recipients = slot === "all" ? teamMembers : teamMembers.filter((m) => m.slot === slot)
    send(res, 200, {
      run: TEAM_RUN,
      recipients,
      delivery_keys: recipients.map((m) => `delivery-e2e-${m.id}`),
    })
    return true
  }
  if (verb === "member_add") {
    rosterVersion++
    send(res, 200, {
      run: TEAM_RUN,
      member: {
        id: `mem-e2e-${rosterVersion}`,
        slot: input.slot,
        actor: "",
        status: "starting",
        kind: "agent",
      },
    })
    return true
  }
  send(res, 200, { run: TEAM_RUN })
  return true
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", "http://fake")
  const path = url.pathname
  const channel = path.match(/^\/channels\/([^/]+)\/messages$/)
  const reply = path.match(/^\/messages\/([^/]+)\/reply$/)
  const delivery = path.match(/^\/messages\/([^/]+)\/delivery$/)
  if (req.method === "GET" && path === "/healthz") return send(res, 200, { ok: true })
  if (req.method === "GET" && path === "/chat/agents") return send(res, 200, { agents })
  if (req.method === "GET" && channel) {
    if (decodeURIComponent(channel[1]) !== CHANNEL)
      return send(res, 404, { error: "no such channel" })
    const since = Number(url.searchParams.get("since") ?? 0)
    const limit = Number(url.searchParams.get("limit") ?? 100) || 100
    const batch = messages.filter((p) => p.seq > since).slice(0, limit)
    const next = batch.length ? batch[batch.length - 1].seq : since
    return send(res, 200, {
      name: CHANNEL,
      address: `msg://service/local/channel/${CHANNEL}`,
      messages: batch,
      next_since: next,
    })
  }
  if (req.method === "GET" && path === "/routing/capabilities") {
    const session = url.searchParams.get("session") ?? url.searchParams.get("session_id") ?? ""
    return send(res, 200, {
      route_supported: true,
      reply_to_sender: session === SESSION_ID,
      interrupt: true,
      kinds_available: ["reply"],
      delivery: "queued",
      runtimes: {},
      session_id: session,
    })
  }
  if (req.method === "POST" && reply) {
    const parent = decodeURIComponent(reply[1])
    if (!messages.some((p) => p.id === parent)) return send(res, 404, { error: "no such message" })
    await body(req)
    const id = `reply-e2e-${replies.size + 1}`
    replies.set(id, { parent, at: new Date().toISOString() })
    return send(res, 200, {
      reply_id: id,
      parent_id: parent,
      state: "delivered",
      target_session_id: SESSION_ID,
    })
  }
  if (req.method === "GET" && delivery) {
    const id = decodeURIComponent(delivery[1])
    const r = replies.get(id)
    if (!r) return send(res, 404, { error: "no such reply" })
    return send(res, 200, {
      reply_id: id,
      parent_id: r.parent,
      state: "delivered",
      original_session_id: SESSION_ID,
      target_session_id: SESSION_ID,
      delivered_to_session_id: SESSION_ID,
      interrupt_requested: false,
      attempts: 1,
      created_at: r.at,
      updated_at: r.at,
      settled_at: r.at,
    })
  }
  // Mail (Agent Ops nudges, B8b). A body containing REFUSE is refused, for the control error path.
  if (req.method === "POST" && path === "/messages") {
    const msg = await body(req)
    const text = JSON.stringify(msg.payload ?? "")
    if (text.includes("REFUSE")) return send(res, 503, { error: "mailbox unavailable" })
    return send(res, 201, { id: `msg-e2e-${Date.now()}` })
  }
  if (await builder(req, res, url)) return
  send(res, 404, { error: "not served by the e2e fake" })
})

// Listens only when run directly (playwright.config.ts); the setup imports the ids above.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const addr = process.env.E2E_TETHER_ADDR ?? "127.0.0.1:28195"
  const at = addr.lastIndexOf(":")
  const host = addr.slice(0, at)
  if (host !== "127.0.0.1") throw new Error("the e2e Tether fake listens on loopback only")
  server.listen(Number(addr.slice(at + 1)), host)
}
