import { useState, type ReactNode } from 'react'
import { useApi } from '@tachyon/torque-client'
import { AlertHost } from '../alerts/AlertHost'
import { CardRuntimeProvider } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { createMailChatClient } from '../chat/mail'
import { ChatProvider } from '../chat/context'
import { HttpHitlClient } from '../hitl/client'
import { registerHitlKinds } from '../hitl/kinds'
import { HitlProvider } from '../hitl/provider'
import { registerMetricCardKinds } from '../metrics/card-kinds'
import { createMailMessagingClient } from '../messaging/adapter'
import { MessagingProvider } from '../messaging/context'
import { OPERATOR_ADDRESS } from '../messaging/operator'
import { AgentUnreadProvider } from '../messaging/unread'
import { DEFAULT_RULE_PACK } from '../rules/default-pack'
import { HttpWorkflowClient } from '../workflow/client'
import { registerWorkflowKinds } from '../workflow/kinds'
import { WorkflowProvider } from '../workflow/provider'
import { ChatDockProvider } from './chat-dock'

/**
 * Everything the Console adds under <ConsoleProvider>, in the one order that works:
 *   Messaging (A2 replies, M1 actions, unread poll) > Chat > card kinds (+ HITL / workflow
 *   clients) > AlertHost > chat dock.
 * AutolinkProvider sits above ConsoleShell (ShellAutolink) so chat refs chip.
 * Tachyon is live-only: Tether mail through the ApiClient (chat has no roster yet:
 * threads open from agent chips / the dock), and the HTTP HITL / workflow clients.
 */
export function ConsoleIntegration({ children }: { children: ReactNode }) {
  const api = useApi()
  const [{ messaging, chat, registry, hitl, workflow }] = useState(() => {
    const messaging = createMailMessagingClient(api, { from: OPERATOR_ADDRESS })
    const chat = createMailChatClient(messaging, { operator: OPERATOR_ADDRESS, agents: [] })
    // Card kinds only: no toast/modal surface (AlertHost owns those).
    const registry = createDefaultCardRegistry()
    registerMetricCardKinds(registry)
    registerHitlKinds(registry)
    registerWorkflowKinds(registry)
    return { messaging, chat, registry, hitl: new HttpHitlClient(), workflow: new HttpWorkflowClient() }
  })
  return (
    <MessagingProvider client={messaging} operator={OPERATOR_ADDRESS}>
      <AgentUnreadProvider>
        <ChatProvider client={chat} operator={OPERATOR_ADDRESS}>
          <CardRuntimeProvider registry={registry} mountSurfaces={false}>
            <HitlProvider client={hitl}>
              <WorkflowProvider client={workflow}>
                <AlertHost rules={DEFAULT_RULE_PACK}>
                  <ChatDockProvider>{children}</ChatDockProvider>
                </AlertHost>
              </WorkflowProvider>
            </HitlProvider>
          </CardRuntimeProvider>
        </ChatProvider>
      </AgentUnreadProvider>
    </MessagingProvider>
  )
}
