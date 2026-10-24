import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@hollis-labs/design-components'
import { MessageSquare, Mail, Clock, GitBranch, Activity } from 'lucide-react'
import type { ChatMessage } from './types'

function Fact({ label, value }: { label: string; value: string | number | undefined }) {
  if (value === undefined || value === '') return null
  return (
    <div className="min-w-0">
      <dt className="font-mono text-[9px] uppercase tracking-widest text-fg-faint">{label}</dt>
      <dd className="mt-1 break-words font-mono text-xs text-fg [overflow-wrap:anywhere]">{value}</dd>
    </div>
  )
}

export function MessageInfo({ message: m, onClose }: { message: ChatMessage; onClose(): void }) {
  const d = m.info
  const Icon = d?.transport === 'tether-chat' ? MessageSquare : Mail
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-4">
            <span className="border-2 border-border bg-bg p-3 text-info">
              <Icon className="h-8 w-8" />
            </span>
            <div className="min-w-0">
              <DialogTitle>Message details</DialogTitle>
              <DialogDescription className="mt-1 font-mono text-[11px]">
                {m.from === 'operator' ? 'You' : (d?.agentName ?? 'Agent')} ·{' '}
                {new Date(m.at).toLocaleString()}
              </DialogDescription>
              <span className="mt-1 inline-block font-mono text-[10px] uppercase tracking-widest text-info">
                {d?.transport === 'tether-chat' ? 'Tether chat' : 'Mailbox message'}
              </span>
            </div>
          </div>
        </DialogHeader>
        <dl className="grid grid-cols-2 gap-x-5 gap-y-4 border-y border-border py-4">
          <Fact label="Message" value={m.id} />
          <Fact label="Delivery" value={m.delivery ?? d?.delivery} />
          <Fact label="Conversation" value={m.threadId} />
          <Fact label="Kind" value={d?.kind} />
          <Fact label="Session" value={d?.sessionId} />
          <Fact label="Turn" value={d?.turnId} />
          <Fact label="Launch" value={d?.launchId} />
          <Fact label="Channel" value={d?.channel} />
          <Fact label="Publication" value={d?.publicationId} />
          <Fact label="Sequence" value={d?.sequence} />
          <Fact label="Runtime" value={d?.runtime} />
          <Fact label="Sender (reported)" value={d?.sender} />
          <Fact label="Output" value={d?.outputId} />
          <Fact label="Idempotency key" value={d?.idempotencyKey} />
        </dl>
        {m.error && (
          <p className="border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-danger">{m.error}</p>
        )}
        {d?.trace?.length ? (
          <section aria-label="Stage trace">
            <h3 className="mb-2 flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-fg-muted">
              <GitBranch className="h-3 w-3" />
              Stage trace
            </h3>
            <ul className="divide-y divide-border border border-border">
              {d.trace.map((t, i) => (
                <li
                  key={`${t.stage_id}:${i}`}
                  className="flex flex-wrap items-center gap-3 px-3 py-2 font-mono text-[11px]"
                >
                  <Activity
                    className={`h-3 w-3 ${t.outcome === 'passed' ? 'text-success' : 'text-warning'}`}
                  />
                  <span className="min-w-0 flex-1 break-words">
                    {t.stage_id} <span className="text-fg-faint">{t.stage_version}</span>
                  </span>
                  <span className={t.outcome === 'passed' ? 'text-success' : 'text-warning'}>
                    {t.outcome}
                  </span>
                  <span className="flex items-center gap-1 text-fg-faint">
                    <Clock className="h-3 w-3" />
                    {t.duration_ms} ms
                  </span>
                  {t.failure_code && <span className="w-full text-danger">{t.failure_code}</span>}
                </li>
              ))}
            </ul>
          </section>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
