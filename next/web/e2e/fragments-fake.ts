/**
 * A local fake of Fragments Engine's transcript API, for the TACHYON_E2E_TRANSCRIPTS=backend run only. It listens on
 * loopback and serves the synthetic transcripts fixtures (e2e/transcripts-fixtures), shaped as Fragments Engine
 * answers the binary's bridge (next/internal/transcripts):
 *
 *   GET /v1/transcripts/status, /facets      from the snapshot fixture
 *   GET /v1/transcripts/sessions[?…]          {items, next_cursor: ""}
 *   GET /v1/transcripts/sessions/{id}[?…]     the session fixture, turns paged by turn_offset/turn_limit
 *
 * A request without the run's bearer (E2E_FRAGMENTS_TOKEN) is 401. Environment: E2E_FRAGMENTS_ADDR host:port.
 */
import { readFileSync } from "node:fs"
import { createServer, type ServerResponse } from "node:http"
import { pathToFileURL } from "node:url"

// biome-ignore lint/suspicious/noExplicitAny: fixture JSON, served as read
const fixture = (name: string): any =>
  JSON.parse(readFileSync(new URL(`./transcripts-fixtures/${name}.json`, import.meta.url), "utf8"))

function send(res: ServerResponse, status: number, body: unknown) {
  res.writeHead(status, { "Content-Type": "application/json" })
  res.end(JSON.stringify(body))
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const snapshot = fixture("snapshot_live")
  const sessions: Record<string, { turns: unknown[] }> = {}
  for (const name of ["session_a", "session_b", "session_c"]) {
    const s = fixture(name)
    sessions[s.session.id] = s
  }
  const token = process.env.E2E_FRAGMENTS_TOKEN ?? ""
  const server = createServer((req, res) => {
    if (!token || req.headers.authorization !== `Bearer ${token}`)
      return send(res, 401, { error: "unauthorized" })
    const url = new URL(req.url ?? "/", "http://fake")
    const path = url.pathname.replace(/^\/v1\/transcripts/, "")
    if (req.method !== "GET") return send(res, 405, { error: "read only" })
    if (path === "/status") return send(res, 200, snapshot.status)
    if (path === "/facets") return send(res, 200, snapshot.facets)
    if (path === "/sessions") return send(res, 200, { items: snapshot.sessions, next_cursor: "" })
    const m = path.match(/^\/sessions\/([a-f0-9]{64})$/)
    if (m && sessions[m[1]]) {
      const s = sessions[m[1]]
      const offset = Number(url.searchParams.get("turn_offset") ?? 0)
      const limit = Number(url.searchParams.get("turn_limit") ?? 50)
      const turns = s.turns.slice(offset, offset + limit)
      const next = offset + limit < s.turns.length ? offset + limit : undefined
      return send(res, 200, {
        ...s,
        turns,
        turn_total: s.turns.length,
        ...(next !== undefined ? { next_turn: next } : { next_turn: undefined }),
      })
    }
    send(res, 404, { error: "no such transcript" })
  })
  const addr = process.env.E2E_FRAGMENTS_ADDR ?? "127.0.0.1:28196"
  const at = addr.lastIndexOf(":")
  const host = addr.slice(0, at)
  if (host !== "127.0.0.1")
    throw new Error("the e2e Fragments Engine fake listens on loopback only")
  server.listen(Number(addr.slice(at + 1)), host)
}
