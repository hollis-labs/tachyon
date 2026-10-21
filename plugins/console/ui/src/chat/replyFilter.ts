import { useCallback, useState } from 'react'
import type { ChatMessage } from './types'

/** 'all' shows every reply; 'mine' hides replies Tether says were written for other agents or the system. */
export type ReplyFilter = 'all' | 'mine'

const KEY = 'tachyon.chat.replyFilter'

function read(): ReplyFilter {
  try { return localStorage.getItem(KEY) === 'mine' ? 'mine' : 'all' } catch { return 'all' }
}

/** The reply filter and its persisted preference. Defaults to all. */
export function useReplyFilter(): [ReplyFilter, (next: ReplyFilter) => void] {
  const [filter, set] = useState<ReplyFilter>(read)
  const update = useCallback((next: ReplyFilter) => {
    set(next)
    try { localStorage.setItem(KEY, next) } catch { /* storage is optional */ }
  }, [])
  return [filter, update]
}

/**
 * Operator messages (and anything that is not an agent reply) always stay.
 * Under 'mine' an agent reply is hidden only when Tether positively says it
 * was written for someone else (audience agent or system). Replies of unknown
 * audience stay: Tether reports unknown whenever the sender used the shared
 * operator credential, which is how the operator's own messages are sent today.
 */
export function isForSomeoneElse(m: ChatMessage): boolean {
  return m.from === 'agent' && (m.audience === 'agent' || m.audience === 'system')
}

export function applyReplyFilter(messages: ChatMessage[], filter: ReplyFilter): ChatMessage[] {
  if (filter === 'all') return messages
  return messages.filter((m) => !isForSomeoneElse(m))
}

/** Agent replies the filter hides. */
export function hiddenReplies(messages: ChatMessage[], filter: ReplyFilter): { hidden: number } {
  if (filter === 'all') return { hidden: 0 }
  return { hidden: messages.filter(isForSomeoneElse).length }
}
