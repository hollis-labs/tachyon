import { useCallback, useState } from 'react'
import type { ChatMessage } from './types'

/** 'all' shows every reply; 'mine' shows only replies Tether proved were asked for by the operator. */
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
 * An agent reply shows under 'mine' only when its audience is proven operator;
 * unknown replies appear under 'all' only.
 */
export function applyReplyFilter(messages: ChatMessage[], filter: ReplyFilter): ChatMessage[] {
  if (filter === 'all') return messages
  return messages.filter((m) => m.from !== 'agent' || m.audience === 'operator')
}

/** Agent replies the filter hides, and how many of those are merely unclassified. */
export function hiddenReplies(messages: ChatMessage[], filter: ReplyFilter): { hidden: number; unclassified: number } {
  if (filter === 'all') return { hidden: 0, unclassified: 0 }
  const hidden = messages.filter((m) => m.from === 'agent' && m.audience !== 'operator')
  return { hidden: hidden.length, unclassified: hidden.filter((m) => !m.audience || m.audience === 'unknown').length }
}
