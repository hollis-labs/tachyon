import { hostFetch } from "@tachyon/host-sdk"
const BASE = "/api/plugins/console/v1/approval-inbox"
interface Status {
  elevated: boolean
  assurance: string
  operator_token_configured: boolean
}
interface Prompt {
  open: boolean
  submitting: boolean
  error: string
}

// The authority and 12-hour expiry remain server-owned. Only prompt state is
// kept here; neither the key nor a fabricated "unlocked" flag is persisted.
export function createOperatorElevation(fetcher: typeof fetch = hostFetch) {
  let state: Prompt = { open: false, submitting: false, error: "" }
  let pending:
    | { promise: Promise<boolean>; resolve(value: boolean): void }
    | undefined
  let connecting: Promise<unknown> | undefined
  const listeners = new Set<() => void>()
  const set = (next: Prompt) => {
    state = next
    listeners.forEach((fn) => fn())
  }
  const finish = (value: boolean) => {
    const p = pending
    pending = undefined
    set({ open: false, submitting: false, error: "" })
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
    return value
  }
  async function ensure() {
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
    set({ open: true, submitting: false, error: "" })
    return promise
  }
  async function submit(token: string) {
    if (!pending || state.submitting) return
    const attempt = pending
    set({ open: true, submitting: true, error: "" })
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
        set({
          open: true,
          submitting: false,
          error:
            res.status === 429
              ? "Too many attempts. Wait a minute and try again."
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
    if (!(await ensure())) return res
    init?.signal?.throwIfAborted()
    // Only a definite pre-action assurance refusal is replayed, exactly once.
    return fetcher(input, init)
  }
  return {
    ensure,
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
