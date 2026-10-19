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
      <dt className="font-mono text-[9px] uppercase tracking-widest text-zinc-500">{label}</dt>
      <dd className="mt-1 break-words font-mono text-xs text-zinc-200 [overflow-wrap:anywhere]">{value}</dd>
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
            <span className="border-2 border-zinc-600 bg-zinc-950 p-3 text-sky-300">
              <Icon className="h-8 w-8" />
            </span>
            <div className="min-w-0">
              <DialogTitle className="font-mono uppercase tracking-wide">Message details</DialogTitle>
              <DialogDescription className="mt-1 font-mono text-[11px]">
                {m.from === 'operator' ? 'You' : (d?.agentName ?? 'Agent')} ·{' '}
                {new Date(m.at).toLocaleString()}
              </DialogDescription>
              <span className="mt-1 inline-block font-mono text-[10px] uppercase tracking-widest text-sky-300">
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
          <p className="border border-red-500/30 bg-red-950/20 px-3 py-2 text-xs text-red-300">{m.error}</p>
        )}
        {d?.trace?.length ? (
          <section aria-label="Stage trace">
            <h3 className="mb-2 flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-zinc-400">
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
                    className={`h-3 w-3 ${t.outcome === 'passed' ? 'text-emerald-300' : 'text-amber-300'}`}
                  />
                  <span className="min-w-0 flex-1 break-words">
                    {t.stage_id} <span className="text-zinc-500">{t.stage_version}</span>
                  </span>
                  <span className={t.outcome === 'passed' ? 'text-emerald-300' : 'text-amber-300'}>
                    {t.outcome}
                  </span>
                  <span className="flex items-center gap-1 text-zinc-500">
                    <Clock className="h-3 w-3" />
                    {t.duration_ms} ms
                  </span>
                  {t.failure_code && <span className="w-full text-red-300">{t.failure_code}</span>}
                </li>
              ))}
            </ul>
          </section>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
