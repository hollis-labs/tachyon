import { useId, useRef, useState } from 'react'
import { Textarea, Button } from '@hollis-labs/design-components'
import { MAX_BODY } from '../messaging/guards'
import { User, Users } from 'lucide-react'
import type { ReplyFilter } from './replyFilter'
import type { ChatCapability } from './types'

const btn = 'border px-3 py-1.5 font-mono dash-label uppercase tracking-widest transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40'

/** Compact chat input. Enter sends, Shift+Enter newline. Reuses M1's body cap. */
export function ChatComposer({ onSend, capability, error, replyFilter, onReplyFilterChange }: { onSend: (body: string) => Promise<void>; capability?: ChatCapability; error?: string | null; replyFilter?: ReplyFilter; onReplyFilterChange?: (next: ReplyFilter) => void }) {
  const id = useId()
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState(false)
  const area = useRef<HTMLTextAreaElement>(null)
  const empty = !body.trim()
  const submit = async () => {
    if (empty || busy || capability?.canSend === false) return
    setBusy(true)
    try {
      await onSend(body.trim())
      setBody('')
    } catch {
      /* error shown via the `error` prop; keep the draft */
    } finally {
      setBusy(false)
      area.current?.focus()
    }
  }
  const note = capability?.mode === 'offline' ? 'Agent is offline. This will queue.' : capability?.mode === 'mail-only' ? 'Mail only. Reply may take a while.' : null
  return (
    <form
      aria-label="Send chat message"
      className="space-y-2 border-t border-border p-3"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <label htmlFor={id} className="sr-only">Message</label>
      <Textarea
        ref={area}
        id={id}
        rows={2}
        value={body}
        maxLength={MAX_BODY}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault()
            void submit()
          }
        }}
        placeholder="Message. Enter to send, Shift+Enter for a new line"
        className="w-full resize-none border border-border bg-bg px-2 py-1.5 font-sans dash-control text-fg-secondary placeholder:text-fg-faint focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      />
      {error && <p role="alert" className="font-mono text-xs text-danger">{error}</p>}
      <div className="flex items-center justify-between gap-3">
        <span className="flex min-w-0 items-center gap-2">
          {replyFilter && onReplyFilterChange && <ReplyFilterToggle value={replyFilter} onChange={onReplyFilterChange} />}
          <span className="truncate font-mono dash-caption text-fg-faint">{note}</span>
        </span>
        <Button type="submit" variant="outline" size="sm" disabled={empty || busy || capability?.canSend === false} className={`${btn} border-primary/60 text-primary hover:bg-primary/10`}>{busy ? 'Sending' : 'Send'}</Button>
      </div>
    </form>
  )
}

/** Icon toggle between every agent reply and only the replies addressed to the operator. */
function ReplyFilterToggle({ value, onChange }: { value: ReplyFilter; onChange: (next: ReplyFilter) => void }) {
  const mine = value === 'mine'
  const Icon = mine ? User : Users
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-pressed={mine}
      aria-label="Only show replies to me"
      title={mine ? 'Hiding replies to other agents. Select to show all replies.' : 'Showing all replies. Select to hide replies to other agents.'}
      onClick={() => onChange(mine ? 'all' : 'mine')}
      className={`gap-1.5 px-2 font-mono dash-caption ${mine ? 'text-primary' : 'text-fg-muted'}`}
    >
      <Icon className="size-3.5" aria-hidden="true" />
      <span>{mine ? 'Replies to me' : 'All replies'}</span>
    </Button>
  )
}
