import { useCallback, useEffect, useState } from 'react'
import { approvalClient, type ApprovalResponse } from '../../approvals/client'
import { chatJSON } from '../roster'
import { demoBlocks } from './fixtures'
import type { Commit } from './Cards'
import type { StreamBlock } from './types'

interface ApiCard {
  item_id: string
  kind: string
  caption?: string
  fields?: { id: string; type: string; label?: string; options?: { id?: string; value?: string; label?: string; description?: string; recommended?: boolean }[] }[]
  actions?: { id: string; label: string }[]
  target?: { ref?: string; op?: string } | null
  state: 'pending' | 'resolved' | 'expired' | 'superseded'
  class?: 'low' | 'medium'
  reversible?: boolean
  result?: { answers?: { value?: unknown }[]; decision?: string } | null
  created_at?: number
}

/** Chat API Card projection -> stream block. Kinds the stream has no block for return null and stay in Approvals. */
export function cardToBlock(c: ApiCard): StreamBlock | null {
  const base = { id: c.item_id, itemId: c.item_id, at: c.created_at ?? 0 }
  const open = c.state === 'pending'
  if (c.state === 'expired' || c.state === 'superseded') return null
  if (c.kind === 'card.approval' || c.kind === 'card.tool_approval') {
    return { ...base, kind: 'approval', title: c.caption ?? 'Approval', tool: c.target?.op ?? c.target?.ref ?? 'tool call', canAlways: c.class === 'low', reversible: c.reversible ?? false, answered: open ? undefined : { decision: c.result?.decision === 'denied' ? 'denied' : 'approved' } }
  }
  const f = c.fields?.find((x) => x.type === 'enum')
  if (c.kind === 'card.question' && f?.options?.length) {
    const options = f.options.map((o, i) => ({ id: o.id ?? o.value ?? String(i), label: o.label ?? o.value ?? o.id ?? '', description: o.description, recommended: o.recommended }))
    const chosen = c.result?.answers?.[0]?.value
    return { ...base, kind: 'decision', title: c.caption ?? f.label ?? 'Decision', options, reversible: c.reversible ?? true, answered: open ? undefined : { optionId: String(chosen ?? options[0].id) } }
  }
  return null
}

/**
 * The blocks between messages for one thread. Live cards come from GET threads/{ref}/cards (read-only here; a failed
 * read is empty, not an error). `?demo=cards` swaps in the golden r2 fixtures so the design is reviewable on the real route.
 */
// Read once at load: the chat route redirects to /chat/<thread> and drops the query string.
export const DEMO = typeof location !== 'undefined' && new URLSearchParams(location.search).get('demo') === 'cards'

export function useBlocks(ref: string | undefined) {
  const demo = DEMO
  const [blocks, setBlocks] = useState<StreamBlock[]>([])
  useEffect(() => {
    if (demo) { setBlocks(demoBlocks(Date.now())); return }
    if (!ref) { setBlocks([]); return }
    let live = true
    const load = () => chatJSON<{ cards: ApiCard[] }>(`/threads/${encodeURIComponent(ref)}/cards`)
      .then((d) => { if (live) setBlocks((d.cards ?? []).map(cardToBlock).filter((b): b is StreamBlock => !!b)) })
      .catch(() => undefined)
    void load()
    const t = setInterval(load, 5000)
    return () => { live = false; clearInterval(t) }
  }, [ref, demo])

  /** Answers go through the Approvals queue, the only record of the decision; demo blocks have no item and stay local. */
  const commit: Commit = useCallback(async (block, value) => {
    if (!block.itemId) return
    const item = await approvalClient.get(block.itemId, true)
    const response = toResponse(block, value, item.request_snapshot.kind)
    await approvalClient.decide(item, response)
  }, [])
  return { blocks, commit }
}

function toResponse(block: StreamBlock, value: unknown, kind: string): ApprovalResponse {
  if (block.kind === 'decision') {
    const v = value as { optionId: string; note?: string }
    return { kind: kind as ApprovalResponse['kind'], decision: 'submitted', note: v.note, answers: [{ questionId: 'choice', value: v.optionId }] }
  }
  const d = value as 'approved' | 'always' | 'denied'
  return { kind: kind as ApprovalResponse['kind'], decision: d === 'denied' ? 'denied' : 'approved', note: d === 'always' ? 'always' : undefined }
}
