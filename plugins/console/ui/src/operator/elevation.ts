import { hostFetch } from "@tachyon/host-sdk"
const BASE = "/api/plugins/console/v1/approval-inbox"
interface Status {
  elevated: boolean
  assurance: string
  operator_token_configured: boolean
  elevation_ttl?: string
  elevation_expires_at?: string
  // Optional until auth hardening (CW-20261030-0082): the UI lights these up when present.
  session?: "active" | "expired" | "none"
  methods?: string[]
  attempts_left?: number
}

export interface StepUp {
  action?: string
  risk?: string
  summary?: string
}

// Tangent owns the lifetime (always | never | <N>d) and the stamped end.
export function elevationCopy(s?: Pick<Status, "elevation_ttl" | "elevation_expires_at">) {
  if (s?.elevation_expires_at) {
    const end = new Date(s.elevation_expires_at)
    if (!Number.isNaN(end.getTime())) return `Access lasts until ${end.toLocaleString()}.`
  }
  const ttl = s?.elevation_ttl
  if (ttl === "always") return "Access lasts until you revoke it or the browser session ends."
  if (ttl === "never") return "Access is not remembered; it lasts about a minute."
  const days = /^([1-9]\d*)d$/.exec(ttl ?? "")?.[1]
  if (days) return `Access lasts ${days === "1" ? "1 day" : days + " days"}.`
  return "Access lifetime is set by the authorization service."
}
export interface Prompt {
  open: boolean
  submitting: boolean
  error: string
  stepUp?: StepUp
  methods?: string[]
  attemptsLeft?: number
  // Estimated end of a lockout (no Retry-After guarantee, not persisted over a restart).
  lockedUntil?: number
}
const IDLE: Prompt = { open: false, submitting: false, error: "" }

// The authority and expiry remain server-owned. Only prompt state is
// kept here; neither the key nor a fabricated "unlocked" flag is persisted.
export function createOperatorElevation(fetcher: typeof fetch = hostFetch) {
  let state: Prompt = IDLE
  let pending:
    | { promise: Promise<boolean>; resolve(value: boolean): void }
    | undefined
  let connecting: Promise<unknown> | undefined
  let lastStatus: Status | undefined
  const listeners = new Set<() => void>()
  const set = (next: Prompt) => {
    state = next
    listeners.forEach((fn) => fn())
  }
  const finish = (value: boolean) => {
    const p = pending
    pending = undefined
    set(IDLE)
    p?.resolve(value)
  }
  async function status(): Promise<Status> {
    const read = () =>
      fetcher(BASE + "/status", {
        headers: { "Cache-Control": "no-store", Accept: "application/json" },
      })
    let res = await read()
    if (res.status === 401 || res.status === 403) {
      connecting ??= fetcher(BASE + "/session", { method: "POST" })
        .then((r) => {
          if (!r.ok) throw new Error("Could not connect the operator session.")
        })
        .finally(() => {
          connecting = undefined
        })
      await connecting
      res = await read()
    }
    if (!res.ok)
      throw new Error(
        "Could not verify the operator session. Try again when the service is available.",
      )
    const value = (await res.json()) as Status
    if (typeof value.elevated !== "boolean")
      throw new Error("Invalid operator session response.")
    lastStatus = value
    return value
  }
  // Read-only session probe for the expired banner; never opens a session or a prompt.
  async function expired(): Promise<boolean> {
    try {
      const res = await fetcher(BASE + "/status", {
        headers: { "Cache-Control": "no-store", Accept: "application/json" },
      })
      if (!res.ok) return false
      const value = (await res.json()) as Status
      lastStatus = value
      return value.session === "expired"
    } catch {
      return false
    }
  }
  async function revoke() {
    const res = await fetcher(BASE + "/de-elevate", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: "{}",
    })
    if (!res.ok) throw new Error("Could not revoke operator access.")
    const value = (await res.json()) as Status
    if (value.elevated !== false) throw new Error("Revocation was not confirmed.")
    lastStatus = value
    return value
  }
  async function ensure(ctx?: { stepUp?: StepUp; methods?: string[] }) {
    const current = await status()
    if (current.elevated && current.assurance === "operator-token") return true
    if (!current.operator_token_configured)
      throw new Error(
        "The operator key is not configured on the authorization service.",
      )
    if (pending) return pending.promise
    let resolve!: (value: boolean) => void
    const promise = new Promise<boolean>((fn) => {
      resolve = fn
    })
    pending = { promise, resolve }
    set({
      open: true,
      submitting: false,
      error: "",
      stepUp: ctx?.stepUp,
      methods: ctx?.methods ?? current.methods,
      attemptsLeft: current.attempts_left,
    })
    return promise
  }
  async function submit(token: string) {
    if (!pending || state.submitting) return
    const attempt = pending
    set({ ...state, open: true, submitting: true, error: "" })
    try {
      const res = await fetcher(BASE + "/elevate", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify({ token }),
      })
      if (pending !== attempt) return
      if (!res.ok) {
        const body = await res.clone().json().catch(() => ({}))
        const retry = Number(res.headers.get("Retry-After"))
        const attemptsLeft =
          typeof body.attempts_left === "number" ? body.attempts_left : undefined
        set({
          ...state,
          open: true,
          submitting: false,
          attemptsLeft,
          lockedUntil:
            res.status === 429
              ? Date.now() + (retry > 0 ? retry : 300) * 1000
              : undefined,
          error:
            res.status === 429
              ? "Too many attempts."
              : res.status >= 500
                ? "The authorization service could not be reached."
                : "That operator key was not accepted.",
        })
        return
      }
      const verified = await status()
      if (!verified.elevated || verified.assurance !== "operator-token")
        throw new Error("Elevation was not confirmed. Please try again.")
      if (pending === attempt) finish(true)
    } catch {
      if (pending === attempt)
        set({
          ...state,
          open: true,
          submitting: false,
          error: "Elevation could not be confirmed. Please try again.",
        })
    }
  }
  async function fetchWithElevation(
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> {
    const res = await fetcher(input, init)
    if (res.status !== 401 && res.status !== 403) return res
    const body = await res
      .clone()
      .json()
      .catch(() => ({}))
    if (
      !["assurance_insufficient", "operator_session_required"].includes(
        body.code,
      )
    )
      return res
    if (!(await ensure({ stepUp: body.step_up, methods: body.methods }))) return res
    init?.signal?.throwIfAborted()
    // Only a definite pre-action assurance refusal is replayed, exactly once.
    return fetcher(input, init)
  }
  return {
    ensure,
    revoke,
    lifetimeCopy: () => elevationCopy(lastStatus),
    lastStatus: () => lastStatus,
    expired,
    status,
    submit,
    cancel: () => finish(false),
    fetch: fetchWithElevation,
    snapshot: () => state,
    subscribe: (fn: () => void) => {
      listeners.add(fn)
      return () => {
        listeners.delete(fn)
      }
    },
  }
}
export const operatorElevation = createOperatorElevation()
export const operatorFetch: typeof fetch = operatorElevation.fetch
