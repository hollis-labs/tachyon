// Server alert -> the view model the tray, toasts and detail already render. The mapping is the only
// place that knows the wire shape, like mapping.ts is for console events.

import type { ItemStatus } from '../../seams/state-store'
import type { AlertEntry } from '../use-alerts'
import type { Alert } from '../types'
import type { ServerAlert } from './types'

export function statusOf(a: ServerAlert): ItemStatus {
  switch (a.status) {
    case 'dismissed':
    case 'settled':
      return a.status
    case 'snoozed':
    case 'deferred':
      return 'snoozed'
    default:
      return a.read ? 'read' : 'unread'
  }
}

const KIND_TITLE: Record<string, string> = {
  'message.failed': 'Message failed',
  'tool.call.finished': 'Tool call finished',
  'tool.call.failed': 'Tool call failed',
  'pr.merged': 'Pull request merged',
  'alert.suppressed': 'Activity events were dropped',
}
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** A known kind reads as a sentence; any other kind loses its dots. Never the raw type string. */
export function kindLabel(kind: string): string {
  const known = KIND_TITLE[kind]
  if (known) return known
  const words = kind.replace(/[._]+/g, ' ').trim()
  return words ? words[0].toUpperCase() + words.slice(1) : kind
}

/** Titles the sources built from a raw kind ("Message message.failed") become sentences. */
export function titleOf(a: ServerAlert): string {
  const title = a.display_title || a.title
  if (a.kind === 'alert.suppressed') return KIND_TITLE['alert.suppressed']
  if (!a.kind || !title.includes(a.kind)) return title
  const label = kindLabel(a.kind)
  const lead = a.kind.split('.')[0]
  if (title.trim().toLowerCase() === `${lead} ${a.kind}`.toLowerCase() || title.trim() === a.kind) return label
  return title.replace(a.kind, label.toLowerCase())
}

/** A bare session id is not a summary: say which session, short. */
export function detailOf(a: ServerAlert): string | undefined {
  const s = (a.summary || '').trim()
  if (!s) return undefined
  return UUID.test(s) ? `in session ${s.slice(0, 8)}` : s
}

export function toAlert(a: ServerAlert): Alert {
  return {
    id: a.alert_id,
    severity: a.severity,
    icon: a.icon,
    title: titleOf(a),
    detail: detailOf(a),
    source: a.source,
    sourceLabel: a.source_label || undefined,
    ts: a.last_at_ms,
    kind: a.kind,
    primary: a.primary ?? undefined,
    coalesceKey: a.coalesce_key || undefined,
    count: a.count > 1 ? a.count : undefined,
    ruleId: a.rule_id,
    needsYou: a.needs_you,
  }
}

export function toEntry(a: ServerAlert): AlertEntry {
  return {
    alert: toAlert(a),
    status: statusOf(a),
    snoozeUntil: a.snooze_until_ms ?? a.wake_at_ms ?? undefined,
    resolution: a.resolution || undefined,
  }
}
