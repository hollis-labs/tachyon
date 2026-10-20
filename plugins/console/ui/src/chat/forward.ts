import type { ForwardRecipient } from '@hollis-labs/kit-chat'
import type { AgentRecord } from '../agents/types'
import type { Recipient } from '../messaging'
import { MAX_BODY } from '../messaging/guards'
import { chatRecipient, type ChatAgent } from './roster'
import type { ChatMessage } from './types'

export interface ForwardTarget extends ForwardRecipient { recipient: Recipient }

/** Launches route through chat; registered agents without a launch use their real mailbox. */
export function forwardTargets(roster: ChatAgent[], directory: AgentRecord[]): ForwardTarget[] {
  const covered = new Set<string>()
  const targets: ForwardTarget[] = roster.flatMap(a => {
    const record = directory.find(r => r.identity.id === a.agent_id || r.launches?.some(l => l.id === a.launch_id))
    if (record && (!record.identity.enabled || record.identity.retired)) return []
    covered.add(a.agent_id)
    if (record) covered.add(record.identity.id)
    const urn = record?.identity.urn
    return [{
      id: chatRecipient(a).address,
      label: a.display_name,
      description: [a.launch_id, a.session?.state ?? 'Not running', a.worker, urn].filter(Boolean).join(' · '),
      keywords: [a.agent_id, a.launch_id, a.channel, a.session?.id ?? '', a.worker ?? '', urn ?? '', record?.runtime.provider ?? '', record?.runtime.project ?? '', ...(record?.launches?.flatMap(l => [l.provider ?? '', l.project ?? '']) ?? []), ...(record?.identity.roles ?? [])],
      recipient: chatRecipient(a),
    }]
  })
  for (const a of directory) {
    const { identity: i } = a
    if (covered.has(i.id) || !i.enabled || i.retired || !i.urn?.startsWith('msg://agent/')) continue
    targets.push({ id: i.urn, label: i.name, description: `${i.urn} · Mail`, keywords: [i.id, ...i.roles, a.runtime.provider, a.runtime.project ?? '', a.runtime.worker ?? ''], recipient: { address: i.urn, label: i.name, kind: 'agent', agentId: i.id } })
  }
  return targets.sort((a, b) => a.label.localeCompare(b.label) || a.id.localeCompare(b.id))
}

export function forwardProblem(target: ForwardTarget, body: string): string | null {
  // Session chat accepts a 64 KiB JSON request. Reserve space for its envelope;
  // mailbox sends retain their existing character limit. Never trim the original.
  const tooLong = target.recipient.chatLaunchId
    ? new TextEncoder().encode(JSON.stringify(body)).length > 60_000
    : body.length > MAX_BODY
  return tooLong ? 'This message is too large to forward in full. Shorten your note or share a document link instead.' : null
}

export function forwardBody(message: ChatMessage, sourceLabel: string, note: string): string {
  const label = sourceLabel.replace(/[\r\n]+/g, ' ')
  const source = [`Forwarded message from ${label}`, `Message: ${message.id}`, message.info?.sessionId ? `Session: ${message.info.sessionId}` : '', '', message.body].filter((line, index) => line || index === 3).join('\n')
  return `${note.trim() ? `${note.trim()}\n\n` : ''}--- Forwarded message ---\n${source}\n--- End forwarded message ---`
}
