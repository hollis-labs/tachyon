import { type Envelope, invokeVerb } from "./verbs"

export interface Repository {
  id: string
  name: string
  path: string
}
export interface RepoStatus {
  branch: string
  head?: string
  upstream?: string
  ahead: number
  behind: number
  dirty: boolean
  changes: string[]
  ci_state: string
}
export interface RepoDetails extends Repository {
  status: RepoStatus
  branches: string[]
}
export interface Commit {
  hash: string
  author: string
  timestamp: string
  subject: string
}
export interface Activity {
  commits: Commit[]
  branches: string[]
}
export interface Diff {
  patch: string
}
export interface DiffRequest {
  id: string
  base?: string
  head?: string
  staged?: boolean
}

export const scmApi = {
  list: () => invokeVerb<Repository[]>("scm_list"),
  read: (id: string) => invokeVerb<RepoDetails>("scm_read", { id }),
  status: (id: string) => invokeVerb<RepoStatus>("scm_status", { id }),
  activity: (id: string, limit: number) => invokeVerb<Activity>("scm_activity", { id, limit }),
  diff: (request: DiffRequest) => invokeVerb<Diff>("scm_diff", request),
}

export function scmData<T>(result: Envelope<T>): T {
  if (result.status === "ok") return result.data
  if (result.status === "error") throw new Error(result.error.message)
  throw new Error(
    `Operator decision required: ${result.ask.prompt}${result.ask.options?.length ? ` (${result.ask.options.join(", ")})` : ""}`,
  )
}

// UTF-8 byte limits, not UTF-16 string length. Never split a code point.
export function boundedPatch(patch: string) {
  const lines = patch ? patch.split("\n") : []
  const candidate = lines.slice(0, 2000).join("\n")
  const encoder = new TextEncoder()
  const bytes = encoder.encode(candidate)
  let end = Math.min(bytes.length, 128 * 1024)
  if (end < bytes.length) {
    while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--
  }
  const text = new TextDecoder().decode(bytes.subarray(0, end))
  return {
    text,
    shownLines: text ? text.split("\n").length : 0,
    totalLines: lines.length,
    shownBytes: end,
    totalBytes: encoder.encode(patch).length,
    truncated: text !== patch,
  }
}
