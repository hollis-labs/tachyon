import { useState, type ReactNode } from 'react'
import {
  Dialog as KitDialog,
  DialogContent,
  DialogTitle,
  DialogTrigger,
  Popover as KitPopover,
  PopoverContent,
  PopoverTrigger,
} from '@hollis-labs/design-components'
import { MessageComposer, type MessageComposerProps } from './MessageComposer'
import type { MessageIntent, Recipient } from './types'

const LABEL: Record<MessageIntent, string> = { message: 'Message', nudge: 'Nudge', update_request: 'Request update' }
const trigger = 'border border-border px-2.5 py-1 font-mono text-[10px] uppercase tracking-widest text-fg-secondary transition-colors hover:border-border hover:text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-warning disabled:cursor-not-allowed disabled:opacity-40'

/** Popover anchored under its trigger (the kit's: focus moves in and returns, Escape and outside clicks close). */
function Popover({ label, disabled, title, children }: { label: string; disabled?: boolean; title?: string; children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)
  return (
    <KitPopover open={open} onOpenChange={setOpen}>
      <PopoverTrigger disabled={disabled} title={title} className={trigger}>{label}</PopoverTrigger>
      <PopoverContent aria-label={label} align="start" className="w-[22rem] max-w-[90vw] p-3">
        {children(close)}
      </PopoverContent>
    </KitPopover>
  )
}

/** Centered dialog for the full composer (the kit's: focus trap, focus return, layered Escape). */
function Dialog({ label, disabled, title, children }: { label: string; disabled?: boolean; title?: string; children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)
  return (
    <KitDialog open={open} onOpenChange={setOpen}>
      <DialogTrigger disabled={disabled} title={title} className={trigger}>{label}</DialogTrigger>
      <DialogContent aria-label={label}>
        <DialogTitle className="sr-only">{label}</DialogTitle>
        {children(close)}
      </DialogContent>
    </KitDialog>
  )
}

type Common = Omit<MessageComposerProps, 'intent' | 'recipient' | 'onCancel'> & { recipient: Recipient | null }
const noRecipient = (r: Recipient | null) => (r ? undefined : 'No resolvable recipient')

export function SendMessageButton(p: Common) {
  return <Dialog label={LABEL.message} disabled={!p.recipient} title={noRecipient(p.recipient)}>{(close) => <MessageComposer {...p} intent="message" onCancel={close} />}</Dialog>
}
export function NudgeButton(p: Common) {
  return <Popover label={LABEL.nudge} disabled={!p.recipient} title={noRecipient(p.recipient)}>{(close) => <MessageComposer {...p} intent="nudge" showThread={false} onCancel={close} />}</Popover>
}
export function RequestUpdateButton(p: Common) {
  return <Popover label={LABEL.update_request} disabled={!p.recipient} title={noRecipient(p.recipient)}>{(close) => <MessageComposer {...p} intent="update_request" showThread={false} onCancel={close} />}</Popover>
}

/** The three actions as one row; mount it in the agent modal, alert cards and agent refs. */
export function AgentMessageActions(p: Common) {
  return (
    <div role="group" aria-label="Message actions" className="flex flex-wrap items-center gap-2">
      <SendMessageButton {...p} />
      <NudgeButton {...p} />
      <RequestUpdateButton {...p} />
    </div>
  )
}
