import { OperatorPrompt } from '../operator/OperatorPrompt'
import { useState, type ReactNode } from 'react'
import { ApprovalProvider } from '../approvals/context'
import { useApi } from '@tachyon/torque-client'
import { AlertHost } from '../alerts/AlertHost'
import { ServerAlertHost } from '../alerts/server/ServerAlertHost'
import { serverAlertsEnabled } from '../alerts/server/source'
import { CardRuntimeProvider } from '../cards/surfaces'
import { createDefaultCardRegistry } from '../cards/kinds'
import { createAppAgentChatClient } from '../chat/appagent'
import { createCompositeChatClient } from '../chat/composite'
import { createTetherChatClient } from '../chat/tether'
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
  const [serverAlerts] = useState(serverAlertsEnabled)
  const [{ messaging, chat, registry, hitl, workflow }] = useState(() => {
    const messaging = createMailMessagingClient(api, { from: OPERATOR_ADDRESS })
    // Tether chat plus Tachyon's own app agent, as ordinary threads in one list.
    const chat = createCompositeChatClient(
      createTetherChatClient(createMailChatClient(messaging, { operator: OPERATOR_ADDRESS, agents: [] })),
      createAppAgentChatClient(),
    )
    // Card kinds only: no toast/modal surface (AlertHost owns those).
    const registry = createDefaultCardRegistry()
    registerMetricCardKinds(registry)
    registerHitlKinds(registry)
    registerWorkflowKinds(registry)
    return { messaging, chat, registry, hitl: new HttpHitlClient(), workflow: new HttpWorkflowClient() }
  })
  const body = <ApprovalProvider><OperatorPrompt /><ChatDockProvider>{children}</ChatDockProvider></ApprovalProvider>
  return (
    <MessagingProvider client={messaging} operator={OPERATOR_ADDRESS}>
      <AgentUnreadProvider>
        <ChatProvider client={chat} operator={OPERATOR_ADDRESS}>
          <CardRuntimeProvider registry={registry} mountSurfaces={false}>
            <HitlProvider client={hitl} present={false}>
              <WorkflowProvider client={workflow}>
                {serverAlerts ? (
                  <ServerAlertHost>{body}</ServerAlertHost>
                ) : (
                  <AlertHost rules={DEFAULT_RULE_PACK}>{body}</AlertHost>
                )}
              </WorkflowProvider>
            </HitlProvider>
          </CardRuntimeProvider>
        </ChatProvider>
      </AgentUnreadProvider>
    </MessagingProvider>
  )
}
