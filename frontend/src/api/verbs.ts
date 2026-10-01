import { useEffect, useMemo, useState } from "react"

export type Envelope<T> =
  | { status: "ok"; data: T }
  | { status: "error"; error: { code: string; message: string; detail?: unknown } }
  | {
      status: "ask"
      ask: { prompt: string; options?: string[]; context?: Record<string, unknown> }
    }

export type VerbRegistry = Record<
  string,
  { modules: string[]; verbs: Record<string, { effect: string }> }
>

// Preserve all contract outcomes, including error and ask on HTTP error
// responses. Callers decide how to display them; only fetch/JSON failures throw.
export async function invokeVerb<T>(verb: string, payload: unknown = {}): Promise<Envelope<T>> {
  const response = await fetch(`/api/verb/${encodeURIComponent(verb)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(payload),
  })
  return response.json() as Promise<Envelope<T>>
}

export async function fetchVerbs(): Promise<VerbRegistry> {
  const response = await fetch("/api/verbs", { headers: { Accept: "application/json" } })
  if (!response.ok) throw new Error(`Failed to load verb registry (HTTP ${response.status})`)
  return response.json() as Promise<VerbRegistry>
}

// Start closed and remain closed if discovery fails. An unmounted consumer
// cannot publish a late response into its replacement's capability state.
export function useVerbs(): {
  loading: boolean
  has(verb: string): boolean
  effect(verb: string): string | undefined
} {
  const [registry, setRegistry] = useState<VerbRegistry>({})
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let active = true
    fetchVerbs()
      .then((declarations) => {
        if (active) setRegistry(declarations)
      })
      .catch(() => {
        if (active) setRegistry({})
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])
  return useMemo(() => {
    const declared = new Map<string, string>()
    for (const capabilities of Object.values(registry ?? {})) {
      if (!capabilities || typeof capabilities.verbs !== "object" || !capabilities.verbs) continue
      for (const [verb, declaration] of Object.entries(capabilities.verbs)) {
        if (typeof declaration?.effect === "string") declared.set(verb, declaration.effect)
      }
    }
    return {
      loading,
      has: (verb: string) => declared.has(verb),
      effect: (verb: string) => declared.get(verb),
    }
  }, [loading, registry])
}
