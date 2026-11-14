import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { Chip } from '@planner/shared'
import { MessageBody } from '../chat/MessageBody'

/** A typed ask as a read-only card (CW-20261114-0023e). Answer buttons wait for Tether's operator-attested replies. */
export interface AskCard {
  kind: string
  subject?: string
  body?: string
  options?: { id: string; label: string; recommended?: boolean; detail?: string }[]
  default_option?: string
  default_at?: string
  re?: string
  awaiting?: string
  reply_to?: string
}

export const CARD = {
  kind: { question: 'Question', decision: 'Decision', approval_request: 'Approval request' } as Record<string, string>,
  recommended: 'Recommended',
  ifNobody: (label: string, at?: string) => `If nobody answers: ${label}${at ? ` (at ${at})` : ''}`,
  answerWith: 'Answer with',
  answerHelp: 'Answering from Tachyon is not available yet. Send one of these from your own Tether CLI or chat session.',
  noHandle: 'the sender\'s @handle',
  copy: 'Copy',
  copied: 'Copied',
} as const

/** The tether_message_post call that answers the ask with one option, sent as a reply so it settles it. */
export function answerCall(card: AskCard, messageId: string, option: { id: string; label: string }) {
  return `tether_message_post ${JSON.stringify({ kind: 'message', to: [card.reply_to || CARD.noHandle], in_reply_to: messageId, answer: { option: option.id }, body: option.label })}`
}

function CopyLine({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-start gap-2">
      <code className="min-w-0 flex-1 break-all rounded-sm bg-surface px-1.5 py-1 font-mono text-caption text-fg">{text}</code>
      <button type="button" aria-label={copied ? CARD.copied : CARD.copy} title={CARD.copy} className="grid size-6 shrink-0 place-items-center rounded-sm text-fg-muted hover:bg-selection hover:text-fg"
        onClick={() => void navigator.clipboard.writeText(text).then(() => setCopied(true), () => undefined)}>
        {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
      </button>
    </div>
  )
}

export function AskCardView({ card, messageId }: { card: AskCard; messageId: string }) {
  const options = card.options ?? []
  const fallback = options.find((o) => o.id === card.default_option)
  return (
    <div data-card={card.kind} className="flex flex-col gap-3 rounded-panel border border-border bg-bg px-3 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <Chip>{CARD.kind[card.kind] ?? card.kind}</Chip>
        {card.subject && <b className="text-control font-semibold text-fg">{card.subject}</b>}
      </div>
      {card.body && <div className="text-control text-fg"><MessageBody body={card.body} /></div>}
      {options.length > 0 && (
        <ul aria-label="Options" className="flex flex-col gap-1.5">
          {options.map((o) => (
            <li key={o.id} data-option={o.id} className={`rounded-control border px-2.5 py-1.5 ${o.recommended ? 'border-fg/40' : 'border-border'}`}>
              <div className="flex items-center gap-2 text-control text-fg">
                <span className="font-medium">{o.label}</span>
                {o.recommended && <Chip tone="strong">{CARD.recommended}</Chip>}
              </div>
              {o.detail && <div className="text-label text-fg-muted">{o.detail}</div>}
            </li>
          ))}
        </ul>
      )}
      {fallback && <p className="text-label text-fg-secondary">{CARD.ifNobody(fallback.label, card.default_at)}</p>}
      {options.length > 0 && (
        <div className="flex flex-col gap-1.5 border-t border-border-subtle pt-2">
          <span className="text-label font-medium text-fg-secondary">{CARD.answerWith}</span>
          <p className="text-label text-fg-muted">{CARD.answerHelp}</p>
          {options.map((o) => <CopyLine key={o.id} text={answerCall(card, messageId, o)} />)}
        </div>
      )}
    </div>
  )
}
