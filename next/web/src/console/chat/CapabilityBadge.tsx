import type { ChatCapability, ChatMode } from './types'

const STYLE: Record<ChatMode, { label: string; dot: string; text: string; hint: string }> = {
  session: { label: 'CHAT', dot: 'bg-success', text: 'text-success', hint: 'Tether session chat' },
  live: { label: 'LIVE', dot: 'bg-success', text: 'text-success', hint: 'Replies stream in real time' },
  'mail-only': { label: 'MAIL ONLY', dot: 'bg-info', text: 'text-info', hint: 'Replies arrive by mail and may be delayed' },
  offline: { label: 'OFFLINE', dot: 'bg-fg-faint', text: 'text-fg-muted', hint: 'Messages queue until the agent connects' },
}

/** Says how the agent can actually be reached. Always visible in the thread header. */
export function CapabilityBadge({ capability, withHint }: { capability: ChatCapability; withHint?: boolean }) {
  const s = STYLE[capability.mode]
  return (
    <span className={`inline-flex items-center gap-2 font-mono text-[10px] tracking-widest ${s.text}`} data-mode={capability.mode} title={capability.detail ?? s.hint}>
      <span className={`h-1.5 w-1.5 ${s.dot}`} aria-hidden />
      {s.label}
      {withHint && <span className="normal-case tracking-normal text-fg-faint">{capability.detail ?? s.hint}</span>}
    </span>
  )
}
