import { Mail, MessageSquare } from 'lucide-react'
import type { FleetAgent } from '@console/fleet/lib/fleet-agents'
import { useAgentUnread } from '@console/messaging/unread-hook'
import { fleetAgentAddress } from '@console/messaging/unread-store'

export function Stat({ label, value, tone }: { label: string; value: string | number; tone?: string }) {
  return (
    <div className="min-w-[3.25rem]">
      <div className={`font-mono text-sm tabular-nums ${tone ?? 'text-fg'}`}>{value}</div>
      <div className="font-mono text-[9px] uppercase tracking-widest text-fg-muted">{label}</div>
    </div>
  )
}

export function Score({ value }: { value: number }) {
  return <span className="font-mono tabular-nums">{String(Math.max(0, value)).padStart(5, '0')}</span>
}

/**
 * Unread mail for the agent. Dimmed when there is none or the source does not report mail.
 * When the feed has no count and `agent` is given, falls back to the Console's batched
 * MessagingClient poll (null, and no poll, while the Console is disabled).
 */
export function MailBadge({ count: feedCount, agent }: { count: number | null; agent?: Pick<FleetAgent, 'id' | 'provider' | 'chat'> }) {
  const fallback = useAgentUnread(feedCount === null && agent && !agent.chat ? fleetAgentAddress(agent) : null)
  if (agent?.chat) return <span className="text-success" title="Tether session chat"><MessageSquare className="h-4 w-4" aria-label="Chat agent" /></span>
  const count = feedCount ?? fallback
  const has = (count ?? 0) > 0
  return (
    <span
      className={`relative inline-flex shrink-0 ${has ? 'text-info' : 'text-fg-faint'}`}
      title={count === null ? 'Messages not reported' : has ? `${count} unread message${count === 1 ? '' : 's'}` : 'No messages'}
    >
      <Mail className="h-4 w-4" aria-label={has ? `${count} unread messages` : 'No messages'} />
      {has && <span className="absolute -right-2 -top-1.5 font-mono text-[9px] leading-none text-info">{count}</span>}
    </span>
  )
}

/** "claude · claude-opus-4-7": the provider, then the exact model when the source reports one. */
export function ModelTag({ agent }: { agent: FleetAgent }) {
  return (
    <span className="shrink-0 truncate font-mono text-[10px] uppercase tracking-wider text-fg-muted" title={agent.model ?? undefined}>
      {agent.chat ? 'chat · tether' : `process · ${agent.provider}`}
      {agent.model && <span className="normal-case tracking-normal text-fg-secondary"> · {agent.model}</span>}
    </span>
  )
}
