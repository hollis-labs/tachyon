import type { ChatAgent } from '../chat/roster'
import { chatRecipient } from '../chat/roster'
import { useEffect, useState } from 'react'
import { useOptionalChatDock } from '../shell/chat-dock'
import { useConsoleEnabled } from '../shell/enabled'
import { useMessaging } from './context'
import { NudgeButton, RequestUpdateButton } from './QuickActions'
import type { Recipient } from './types'
import { fleetAgentAddress } from './unread-store'

const BTN = 'border border-border px-2.5 py-1 font-mono text-[10px] uppercase tracking-widest text-fg-secondary transition-colors hover:border-border hover:text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-warning disabled:cursor-not-allowed disabled:opacity-40'

/**
 * Nudge / request update / chat for one fleet agent, for the agent modal. Renders nothing
 * unless the Console is enabled. `onBeforeChat` lets the host close its dialog so the
 * chat drawer is not left behind it. (Full "Message" stays in the chat drawer: a fixed
 * dialog inside the modal's transformed container would mis-position.)
 */
export function FleetMessageActions({ agent, onBeforeChat }: { agent: { id: string; provider: string; chat?: ChatAgent }; onBeforeChat?: () => void }) {
  const enabled = useConsoleEnabled()
  const dock = useOptionalChatDock()
  const { client } = useMessaging()
  const address = fleetAgentAddress(agent)
  const [recipient, setRecipient] = useState<Recipient | null>(null)
  useEffect(() => {
    if (!enabled) return
    if (agent.chat) { setRecipient(chatRecipient(agent.chat)); return }
    let live = true
    client.resolveRecipient(address).then((r) => live && setRecipient(r), () => live && setRecipient(null))
    return () => {
      live = false
    }
  }, [enabled, client, address, agent.chat])
  if (!enabled) return null
  return (
    <section aria-label="Message this agent" className="mt-3">
      <h3 className="mb-1 font-mono text-[10px] uppercase tracking-widest text-fg-muted">Message</h3>
      <div className="flex flex-wrap items-center gap-2">
        {!agent.chat && <><NudgeButton recipient={recipient} /><RequestUpdateButton recipient={recipient} /></>}
        <button
          type="button"
          disabled={!recipient || !dock}
          title={recipient ? undefined : 'No resolvable recipient'}
          onClick={() => {
            onBeforeChat?.()
            dock?.openChat(recipient)
          }}
          className={BTN}
        >
          {agent.chat ? 'Chat' : 'Email'}
        </button>
      </div>
    </section>
  )
}
