import { useEffect, useMemo, useState } from 'react'
import { ChatStream, type ChatItem } from '@hollis-labs/kit-chat'
import { OverflowMenu } from '@hollis-labs/design-components'
import { ForwardDialog } from './ForwardDialog'
import { Bookmark, Check, Copy, Hash, Link2 } from 'lucide-react'
import { MessageInfo } from './MessageInfo'
import { MessageBody } from './MessageBody'
import { DeliveryBadge, ObservedDeliveryBadge } from '../messaging/DeliveryStatus'
import { ToolChips } from './ToolChips'
import type { ChatMessage } from './types'
import { ApprovalCard, DecisionCard, DispatchCard, PlanCard, ToolCallsRow, type Commit } from './cards/Cards'
import { RunCard } from './cards/RunCard'
import type { StreamBlock } from './cards/types'

function BlockView({ b, commit, onOpenPanel }: { b: StreamBlock; commit?: Commit; onOpenPanel?: () => void }) {
  switch (b.kind) {
    case 'decision': return <DecisionCard block={b} commit={commit} />
    case 'plan': return <PlanCard block={b} onOpenPanel={onOpenPanel} />
    case 'approval': return <ApprovalCard block={b} commit={commit} />
    case 'dispatch': return <DispatchCard block={b} commit={commit} />
    case 'tools': return <ToolCallsRow block={b} />
    case 'run': return <RunCard run={b.run} />
  }
}

function Bubble({ m, agentLabel }: { m: ChatMessage; agentLabel: string }) {
  const mine = m.from === 'operator'
  const [info, setInfo] = useState(false)
  const [forward, setForward] = useState(false)
  const [forwarded, setForwarded] = useState('')
  const [copied, setCopied] = useState(false)
  const [actionError, setActionError] = useState('')
  const copy = async () => {
    try { await navigator.clipboard.writeText(m.body); setCopied(true); setActionError('') }
    catch { setActionError('Could not copy. Check clipboard permissions.') }
  }
  const download = () => {
    const url = URL.createObjectURL(new Blob([m.body], { type: 'text/markdown;charset=utf-8' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${m.from}-${new Date(m.at).toISOString().replace(/[:.]/g, '-')}.md`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  const [bookmarked, setBookmarked] = useState(false)
  const [topic, setTopic] = useState(false)
  const [linked, setLinked] = useState(false)
  const link = async () => {
    try { await navigator.clipboard.writeText(`${location.origin}${location.pathname}#${m.id}`); setLinked(true); setActionError('') }
    catch { setActionError('Could not copy the link. Check clipboard permissions.') }
  }
  const label = mine ? 'You' : m.from === 'system' ? 'System' : m.info?.agentName || agentLabel
  const time = new Date(m.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
  const sending = mine && m.delivery === 'queued'
  // Bookmark and topic are local until the chat API's bookmark/topic writes are wired (C-D); the toolbar is the golden's.
  const tb = 'grid h-[22px] w-6 place-items-center rounded-sm text-fg-muted hover:bg-selection hover:text-fg focus-visible:bg-selection focus-visible:outline-none'
  const toolbar = (
    <div role="toolbar" aria-label="Message actions" className="absolute -top-1.5 right-0 z-10 hidden gap-0.5 rounded-control border border-border bg-bg-elevated p-0.5 group-focus-within:flex group-hover:flex">
      <button type="button" className={tb} aria-pressed={bookmarked} aria-label={bookmarked ? 'Remove bookmark' : 'Bookmark'} title="Bookmark (B)" onClick={() => setBookmarked((v) => !v)}><Bookmark className="size-3" /></button>
      <button type="button" className={tb} aria-pressed={topic} aria-label="Add topic" title="Add topic" onClick={() => setTopic((v) => !v)}><Hash className="size-3" /></button>
      <button type="button" className={tb} aria-label={linked ? 'Link copied' : 'Copy link'} title="Copy link" onClick={() => void link()}>{linked ? <Check className="size-3" /> : <Link2 className="size-3" />}</button>
      <button type="button" className={tb} aria-label={copied ? 'Message copied' : 'Copy message'} title="Copy message" onClick={() => void copy()}>{copied ? <Check className="size-3" /> : <Copy className="size-3" />}</button>
      <OverflowMenu ariaLabel="More message actions" align="end" actions={[
        { label: 'Forward to agent', onSelect: () => setForward(true), disabled: !m.body.trim() || m.streaming },
        { label: 'Download Markdown', onSelect: download },
        { label: 'Message details', onSelect: () => setInfo(true) },
      ]} />
    </div>
  )
  return (
    <article id={m.id} data-from={m.from} data-streaming={m.streaming ? 'true' : undefined} className={`group relative mx-auto w-full min-w-0 max-w-[760px] ${mine ? 'flex flex-col items-end' : ''}`}>
      {bookmarked && <Bookmark aria-label="Bookmarked" className="absolute -left-[18px] top-[3px] size-3 text-fg-secondary max-sm:hidden" />}
      {mine ? (
        <div className={`max-w-[70%] rounded-panel bg-surface px-3 py-2 text-control ${sending ? 'opacity-60' : ''}`}>
          {m.body && <MessageBody body={m.body} streaming={m.streaming} />}
        </div>
      ) : (
        <>
          <div className="flex items-center gap-2">
            <span className="font-mono text-caption uppercase tracking-label text-fg-muted">{label}</span>
            <span className="text-label text-fg-muted">· {time}</span>
            {m.streaming && <span className="text-label text-fg-muted">● Responding…</span>}
          </div>
          {toolbar}
          {m.body && <MessageBody body={m.body} streaming={m.streaming} />}
        </>
      )}
      {mine && <div className="relative">{toolbar}</div>}
      <div className="flex items-center gap-2 text-label text-fg-muted">
        {mine && <span>{sending ? 'Sending' : m.delivery === 'failed' ? 'Not sent' : ''}</span>}
        {mine && m.delivery && m.delivery !== 'queued' && m.delivery !== 'failed' && (m.info?.transport === 'mail' ? <ObservedDeliveryBadge messageId={m.id} state={m.delivery} /> : <DeliveryBadge state={m.delivery} detail="Last known chat transport status. Delivered does not mean read." />)}
      </div>
      <ToolChips tools={m.tools} />
      {actionError && <p role="alert" className="mt-1 font-mono text-xs text-danger">{actionError}</p>}
      {forwarded && <p role="status" className="mt-2 text-caption text-success">Forwarded to {forwarded}</p>}
      {forward && <ForwardDialog message={m} sourceLabel={label} onClose={() => setForward(false)} onSent={setForwarded} />}
      {info && <MessageInfo message={m} onClose={() => setInfo(false)} />}
      {m.error && <p role="alert" className="mt-1 font-mono text-xs text-danger">{m.error}</p>}
    </article>
  )
}

const renderMessage = (item: ChatItem) => item.kind === 'message' ? item.content : null

export function ThreadView({ messages, agentLabel = 'Agent', loading, hasMore, onLoadOlder, empty, blocks = [], commit, onOpenPanel }: { messages: ChatMessage[]; agentLabel?: string; loading: boolean; hasMore: boolean; onLoadOlder: () => void; empty?: React.ReactNode; blocks?: StreamBlock[]; commit?: Commit; onOpenPanel?: () => void }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  const items = useMemo<ChatItem[]>(() => {
    const all = [
      ...messages.map((m) => ({ at: m.at, item: { kind: 'message', id: m.id, role: m.from === 'operator' ? 'user' : m.from === 'system' ? 'system' : 'assistant', content: <Bubble m={m} agentLabel={agentLabel} /> } as ChatItem })),
      ...blocks.map((b) => ({ at: b.at, item: { kind: 'message', id: `block-${b.id}`, role: 'assistant', content: <BlockView b={b} commit={commit} onOpenPanel={onOpenPanel} /> } as ChatItem })),
    ]
    return all.sort((a, b) => a.at - b.at).map((x) => x.item)
  }, [messages, blocks, commit, onOpenPanel, now, agentLabel])
  return <ChatStream items={items} renderItem={renderMessage} loading={loading && !messages.length}
    history={{ hasOlder: hasMore, loading: loading && !!messages.length, onLoadOlder }}
    viewportClassName="flex flex-col gap-4 px-6 pb-3 pt-5 max-sm:px-3" empty={empty ?? <p className="text-control text-fg-muted">No messages yet. Say something.</p>} />
}
