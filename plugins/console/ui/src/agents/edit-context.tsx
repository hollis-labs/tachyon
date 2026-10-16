import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { SeamsContext } from '@console/events/context'
import { createActionBus, HttpIdentityConfirm, MockIdentityConfirm, type ActionBus, type IdentityConfirm } from '@console/seams'
import { registerAgentActions } from './actions'
import { useAgentsClient } from './context'

export interface AgentEditValue {
  bus: ActionBus
  identity: IdentityConfirm
}

const EditContext = createContext<AgentEditValue | null>(null)

// eslint-disable-next-line react-refresh/only-export-components
export function resolveStepUp(
  seams: { actionBus: ActionBus; identity: IdentityConfirm } | null,
  props: { bus?: ActionBus; identity?: IdentityConfirm; allowAutoGrant?: boolean },
): AgentEditValue {
  const { bus, identity, allowAutoGrant } = props
  if (bus && identity) return { bus, identity }
  if (seams) {
    const installed = identity ?? seams.identity
    if (!allowAutoGrant && installed instanceof MockIdentityConfirm && installed.mode === 'auto-grant') {
      const manual = new MockIdentityConfirm({ mode: 'manual' })
      return { bus: createActionBus({ identity: manual }), identity: manual }
    }
    return { bus: bus ?? (identity ? createActionBus({ identity }) : seams.actionBus), identity: installed }
  }
  const id = identity ?? new HttpIdentityConfirm()
  return { bus: bus ?? createActionBus({ identity: id }), identity: id }
}

export function AgentEditProvider({ bus, identity, allowAutoGrant, children }: { bus?: ActionBus; identity?: IdentityConfirm; allowAutoGrant?: boolean; children: ReactNode }) {
  const seams = useContext(SeamsContext)
  const client = useAgentsClient()
  const value = useMemo<AgentEditValue>(() => resolveStepUp(seams, { bus, identity, allowAutoGrant }), [bus, identity, allowAutoGrant, seams])
  useEffect(() => registerAgentActions(value.bus, client), [value.bus, client])
  return <EditContext.Provider value={value}>{children}</EditContext.Provider>
}

/** Fallback keeps the directory working when the provider is absent (tests, previews). */
// eslint-disable-next-line react-refresh/only-export-components
export function useAgentEdit(): AgentEditValue | null {
  return useContext(EditContext)
}
