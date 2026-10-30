import { sessionThreadId } from '../chatapi'
import { useChatThread } from '../hooks'
import { useBlocks } from '../cards/useBlocks'
import { applyReplyFilter, hiddenReplies, useReplyFilter, type ReplyFilter } from '../replyFilter'
import { ReplyFilterToggle } from '../ReplyFilterToggle'
import type { ChatMessage, ChatThread } from '../types'
import type { PaletteActions } from '../workbench/Palette'
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
export function ThreadConversation({ thread, palette }: { thread: ChatThread; palette?: PaletteActions }) {
  // The agent's newest chat API session (or the one picked in the details) is the conversation when there is one.
  const ref = thread.sessionRef ?? thread.sessions?.[0]?.ref
  const convId = ref ? sessionThreadId(ref) : thread.id
  const { messages, capability, loading, hasMore, loadOlder, send, sendError, error } = useChatThread(convId)
  const { blocks, commit } = useBlocks(ref)
  const cap = capability ?? thread.capability
  const [replyFilter, setReplyFilter] = useReplyFilter()
  const shown = applyReplyFilter(messages, replyFilter)
  const note = cap.mode === 'offline' ? 'Agent is offline. This will queue.' : cap.mode === 'mail-only' ? 'Mail only. Reply may take a while.' : null
  return (
    <ConversationView
      threadId={convId}
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
      palette={palette}
      blocks={blocks}
      commit={commit}
      toolbarStart={<ReplyFilterToggle value={replyFilter} onChange={setReplyFilter} />}
      empty={messages.length > 0 ? <FilteredEmpty messages={messages} filter={replyFilter} hasMore={hasMore} onShowAll={() => setReplyFilter('all')} onLoadOlder={() => void loadOlder()} /> : undefined}
    />
  )
}
