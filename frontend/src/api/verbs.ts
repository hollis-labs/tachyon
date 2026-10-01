import {
  createContext,
  createElement,
  type PropsWithChildren,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react"

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

export class RegistryUnavailableError extends Error {}

export async function fetchVerbs(): Promise<VerbRegistry> {
  const response = await fetch("/api/verbs", { headers: { Accept: "application/json" } })
  if (response.status === 404 || response.status === 501)
    throw new RegistryUnavailableError("Verb registry is unavailable")
  if (!response.ok) throw new Error(`Failed to load verb registry (HTTP ${response.status})`)
  return response.json() as Promise<VerbRegistry>
}

interface VerbState {
  registry: VerbRegistry
  loading: boolean
  available: boolean | null
}
const VerbContext = createContext<VerbState | null>(null)

function useVerbDiscovery(enabled = true): VerbState {
  const [state, setState] = useState<VerbState>({ registry: {}, loading: true, available: null })
  useEffect(() => {
    if (!enabled) return
    let active = true
    fetchVerbs()
      .then((registry) => {
        if (active) setState({ registry, loading: false, available: true })
      })
      .catch((error) => {
        if (active)
          setState({
            registry: {},
            loading: false,
            available: error instanceof RegistryUnavailableError ? false : null,
          })
      })
    return () => {
      active = false
    }
  }, [enabled])
  return state
}

// Share one discovery result across the shell and pages. Standalone consumers
// can still use useVerbs without a provider.
export function VerbProvider({ children }: PropsWithChildren) {
  const state = useVerbDiscovery()
  return createElement(VerbContext.Provider, { value: state }, children)
}

export function useVerbs(): {
  loading: boolean
  available: boolean | null
  registry: VerbRegistry
  has(verb: string): boolean
  supports(module: string, verb: string): boolean
  effect(verb: string): string | undefined
} {
  const shared = useContext(VerbContext)
  const standalone = useVerbDiscovery(!shared)
  const { registry, loading, available } = shared ?? standalone
  return useMemo(() => {
    const declared = new Map<string, string>()
    for (const [module, capabilities] of Object.entries(registry ?? {})) {
      if (!capabilities || typeof capabilities.verbs !== "object" || !capabilities.verbs) continue
      for (const [verb, declaration] of Object.entries(capabilities.verbs)) {
        if (verb.startsWith(`${module}_`) && typeof declaration?.effect === "string")
          declared.set(verb, declaration.effect)
      }
    }
    return {
      loading,
      available,
      registry,
      has: (verb: string) => declared.has(verb),
      supports: (module: string, verb: string) =>
        verb.startsWith(`${module}_`) && declared.has(verb),
      effect: (verb: string) => declared.get(verb),
    }
  }, [loading, available, registry])
}
