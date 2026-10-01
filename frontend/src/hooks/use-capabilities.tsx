import {
  createContext,
  type PropsWithChildren,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react"
import type { AgentCapabilities } from "../api/client"
import { useApi } from "../api/context"
import { useVerbs, VerbProvider } from "../api/verbs"

type LegacyFlag = Exclude<keyof AgentCapabilities, "provider">
const LegacyContext = createContext<AgentCapabilities | null>(null)

export function CapabilitiesProvider({ children }: PropsWithChildren) {
  const api = useApi()
  const [legacy, setLegacy] = useState<AgentCapabilities | null>(null)
  useEffect(() => {
    let active = true
    api
      .getCapabilities()
      .then((capabilities) => {
        if (active) setLegacy(capabilities)
      })
      .catch(() => {
        if (active) setLegacy(null)
      })
    return () => {
      active = false
    }
  }, [api])
  return (
    <LegacyContext.Provider value={legacy}>
      <VerbProvider>{children}</VerbProvider>
    </LegacyContext.Provider>
  )
}

const fallbackFlags: Record<string, LegacyFlag> = {
  agent_create: "can_create",
  agent_update: "can_update",
  agent_delete: "can_delete",
  agent_grant: "can_grant_tools",
  agent_revoke: "can_grant_tools",
  agent_approve: "can_assign_skills",
  agent_reflex_create: "can_manage_reflexes",
  agent_reflex_update: "can_manage_reflexes",
  agent_reflex_delete: "can_manage_reflexes",
}

// Registry declarations are authoritative, including an empty successful
// registry. Legacy booleans apply only when the endpoint is absent (404/501),
// never during loading, network errors, or server errors.
export function useCapabilities() {
  const legacy = useContext(LegacyContext)
  const verbs = useVerbs()
  return useMemo(() => {
    function has(verb: string, legacyFlag?: LegacyFlag): boolean {
      if (verbs.available !== false) return verbs.has(verb)
      const flag = legacyFlag ?? fallbackFlags[verb]
      return !!legacy && (flag ? legacy[flag] : verb === "agent_list" || verb === "agent_read")
    }
    const agent: AgentCapabilities = {
      provider: legacy?.provider ?? "",
      can_create: has("agent_create"),
      can_update: has("agent_update"),
      can_delete: has("agent_delete"),
      can_grant_tools: has("agent_grant") && has("agent_revoke"),
      can_assign_skills:
        has("agent_grant", "can_assign_skills") && has("agent_revoke", "can_assign_skills"),
      can_attach_mcp_servers:
        has("agent_grant", "can_attach_mcp_servers") &&
        has("agent_revoke", "can_attach_mcp_servers"),
      can_manage_reflexes:
        has("agent_reflex_create") && has("agent_reflex_update") && has("agent_reflex_delete"),
    }
    return { ...verbs, has, agent }
  }, [legacy, verbs])
}
