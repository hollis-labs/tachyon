import { AgentPresence } from '../../observations/AgentPresence'
import { CapabilityBadge } from '../CapabilityBadge'
import { useChatThread } from '../hooks'
import { applyReplyFilter, hiddenReplies, useReplyFilter, type ReplyFilter } from '../replyFilter'
import { ReplyFilterToggle } from '../ReplyFilterToggle'
import type { ChatMessage, ChatThread } from '../types'
import { ConversationView } from './ConversationView'

/** Shown when the reply filter hides everything that has loaded so far. */
function FilteredEmpty({ messages, filter, hasMore, onShowAll, onLoadOlder }: { messages: ChatMessage[]; filter: ReplyFilter; hasMore: boolean; onShowAll: () => void; onLoadOlder: () => void }) {
  const { hidden } = hiddenReplies(messages, filter)
  return (
    <div role="status" className="space-y-2 text-control text-fg-muted">
      <p>Nothing here besides replies to other agents{hasMore ? ' in what has loaded so far' : ''}.</p>
      {hidden > 0 && (
        <p>
          {hidden} {hidden === 1 ? 'reply to another agent is' : 'replies to other agents are'} hidden.
        </p>
      )}
      <div className="flex gap-3">
        <button type="button" onClick={onShowAll} className="underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">Show all replies</button>
        {hasMore && <button type="button" onClick={onLoadOlder} className="underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">Load older messages</button>}
      </div>
    </div>
  )
}

/**
 * One thread's live conversation: history, streaming, send, reply filter, reachability.
 * Used by the /chat page and the dock. Key it on the thread id so state starts fresh.
 */
export function ThreadConversation({ thread }: { thread: ChatThread }) {
  const { messages, capability, loading, hasMore, loadOlder, send, sendError, error } = useChatThread(thread.id)
  const cap = capability ?? thread.capability
  const [replyFilter, setReplyFilter] = useReplyFilter()
  const shown = applyReplyFilter(messages, replyFilter)
  const note = cap.mode === 'offline' ? 'Agent is offline. This will queue.' : cap.mode === 'mail-only' ? 'Mail only. Reply may take a while.' : null
  return (
    <ConversationView
      threadId={thread.id}
      messages={shown}
      agentLabel={thread.title}
      loading={loading}
      hasMore={hasMore}
      onLoadOlder={() => void loadOlder()}
      onSend={send}
      canSend={cap.canSend !== false}
      error={error}
      sendError={sendError}
      note={note}
      status={
        <div className="space-y-1 border-b border-border px-3 py-2">
          <CapabilityBadge capability={cap} withHint />
          <AgentPresence sessionId={cap.sessionId} address={thread.agent.address} />
        </div>
      }
      toolbarStart={<ReplyFilterToggle value={replyFilter} onChange={setReplyFilter} />}
      empty={messages.length > 0 ? <FilteredEmpty messages={messages} filter={replyFilter} hasMore={hasMore} onShowAll={() => setReplyFilter('all')} onLoadOlder={() => void loadOlder()} /> : undefined}
    />
  )
}
