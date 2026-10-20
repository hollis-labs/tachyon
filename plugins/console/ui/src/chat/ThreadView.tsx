import { useEffect, useMemo, useState } from 'react'
import { ChatStream, MessageAction, MessageActions, type ChatItem } from '@hollis-labs/kit-chat'
import { ForwardDialog } from './ForwardDialog'
import { Copy, Download, Info, Check, Forward } from 'lucide-react'
import { MessageInfo } from './MessageInfo'
import { ago } from '@console/lib/agent-activity'
import { MessageBody } from './MessageBody'
import { DeliveryBadge } from '../messaging'
import { ToolChips } from './ToolChips'
import type { ChatMessage } from './types'

function Bubble({ m, now, agentLabel }: { m: ChatMessage; now: number; agentLabel: string }) {
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
  return (
    <article data-from={m.from} data-streaming={m.streaming ? 'true' : undefined} className={`min-w-0 border-b border-border/70 px-1 pb-5 ${mine ? 'rounded-panel bg-surface p-3' : ''}`}>
      <div className="flex items-center justify-between gap-3 font-mono dash-caption uppercase tracking-widest text-fg-muted">
        <span>{mine ? 'you' : m.from === 'system' ? 'system' : 'agent'} · {ago(m.at, now)}</span>
        {mine && m.delivery && <DeliveryBadge state={m.delivery} />}
        <MessageActions className="ml-auto text-fg-muted">
          <MessageAction label={copied ? 'Message copied' : 'Copy message'} tooltip="Copy message" onClick={() => void copy()}>{copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}</MessageAction>
          <MessageAction label="Forward to agent" tooltip={m.streaming ? 'Wait for the response to finish' : 'Forward to agent'} disabled={!m.body.trim() || m.streaming} onClick={() => setForward(true)}><Forward className="size-3.5" /></MessageAction>
          <MessageAction label="Download message" tooltip="Download Markdown" onClick={download}><Download className="size-3.5" /></MessageAction>
          <MessageAction label="Message details" tooltip="Message details" onClick={() => setInfo(true)}><Info className="size-3.5" /></MessageAction>
        </MessageActions>
        {m.streaming && <span className="animate-pulse text-warning">STREAMING</span>}
      </div>
      {m.body && (
        <MessageBody body={m.body} streaming={m.streaming} />
      )}
      <ToolChips tools={m.tools} />
      {actionError && <p role="alert" className="mt-1 font-mono text-xs text-danger">{actionError}</p>}
      {forwarded && <p role="status" className="mt-2 text-caption text-success">Forwarded to {forwarded}</p>}
      {forward && <ForwardDialog message={m} sourceLabel={mine ? 'You' : m.from === 'system' ? 'System' : m.info?.agentName || agentLabel} onClose={() => setForward(false)} onSent={setForwarded} />}
      {info && <MessageInfo message={m} onClose={() => setInfo(false)} />}
      {m.error && <p role="alert" className="mt-1 font-mono text-xs text-danger">{m.error}</p>}
    </article>
  )
}

const renderMessage = (item: ChatItem) => item.kind === 'message' ? item.content : null

export function ThreadView({ messages, agentLabel = 'Agent', loading, hasMore, onLoadOlder }: { messages: ChatMessage[]; agentLabel?: string; loading: boolean; hasMore: boolean; onLoadOlder: () => void }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  const items = useMemo<ChatItem[]>(() => messages.map(m => ({
    kind: 'message', id: m.id, role: m.from === 'operator' ? 'user' : m.from === 'system' ? 'system' : 'assistant',
    content: <Bubble m={m} now={now} agentLabel={agentLabel} />,
  })), [messages, now, agentLabel])
  return <ChatStream items={items} renderItem={renderMessage} loading={loading && !messages.length}
    history={{ hasOlder: hasMore, loading: loading && !!messages.length, onLoadOlder }}
    viewportClassName="px-4 py-4" empty={<p className="text-control text-fg-muted">No messages yet. Say something.</p>} />
}
