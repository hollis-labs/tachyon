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
 * Shapes follow mesh/core tetherclient (ChannelMessage, RoutingCapabilitiesResponse, ReplyReceipt,
 * ReplyDelivery). Environment: E2E_TETHER_ADDR host:port to listen on.
 */
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

const messages = [
  publication(1, "question", "Shall I start on the chat port?", T0),
  publication(2, "final", "The planner surface is ready for review.", T0 + 60_000),
]
const replies = new Map<string, { parent: string; at: string }>()

function send(res: ServerResponse, status: number, body: unknown) {
  res.writeHead(status, { "Content-Type": "application/json" })
  res.end(JSON.stringify(body))
}

async function body(req: IncomingMessage): Promise<Record<string, unknown>> {
  let raw = ""
  for await (const chunk of req) raw += chunk
  return raw ? JSON.parse(raw) : {}
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
