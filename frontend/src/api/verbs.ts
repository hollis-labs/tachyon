import {
  createContext,
  createElement,
  type PropsWithChildren,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"

export interface AskDetail {
  prompt: string
  options?: string[]
  context?: Record<string, unknown>
  kind?: string
  item_id?: string
  item_url?: string
  operation_id?: string
  state?: string
  expiry?: string
  continuation?: string
  unavailable?: string
}

export type Envelope<T> =
  | { status: "ok"; data: T }
  | { status: "error"; error: { code: string; message: string; detail?: unknown } }
  | {
      status: "ask"
      ask: AskDetail
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
type VerbDiscovery = VerbState & { refresh(): Promise<void>; refreshError: string | null }
const VerbContext = createContext<VerbDiscovery | null>(null)

function useVerbDiscovery(enabled = true): VerbDiscovery {
  const [state, setState] = useState<VerbState>({ registry: {}, loading: true, available: null })
  const [refreshError, setRefreshError] = useState<string | null>(null)
  const mounted = useRef(false)
  const sequence = useRef(0)
  const flight = useRef<Promise<void> | null>(null)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      sequence.current += 1
    }
  }, [])
  const refresh = useCallback(() => {
    if (flight.current) return flight.current
    const version = ++sequence.current
    if (mounted.current) setRefreshError(null)
    const pending = fetchVerbs()
      .then((registry) => {
        if (mounted.current && sequence.current === version) {
          setState({ registry, loading: false, available: true })
        }
      })
      .catch((error: unknown) => {
        if (mounted.current && sequence.current === version) {
          setRefreshError(error instanceof Error ? error.message : "Verb refresh failed")
        }
        throw error
      })
      .finally(() => {
        if (flight.current === pending) flight.current = null
      })
    flight.current = pending
    return pending
  }, [])
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
  return { ...state, refresh, refreshError }
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
  refresh(): Promise<void>
  refreshError: string | null
  has(verb: string): boolean
  supports(module: string, verb: string): boolean
  effect(verb: string): string | undefined
} {
  const shared = useContext(VerbContext)
  const standalone = useVerbDiscovery(!shared)
  const { registry, loading, available, refresh, refreshError } = shared ?? standalone
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
      refresh,
      refreshError,
      has: (verb: string) => declared.has(verb),
      supports: (module: string, verb: string) =>
        verb.startsWith(`${module}_`) && declared.has(verb),
      effect: (verb: string) => declared.get(verb),
    }
  }, [loading, available, registry, refresh, refreshError])
}
