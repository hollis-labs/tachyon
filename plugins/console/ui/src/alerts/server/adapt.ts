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

export function toAlert(a: ServerAlert): Alert {
  return {
    id: a.alert_id,
    severity: a.severity,
    icon: a.icon,
    title: a.display_title || a.title,
    detail: a.summary || undefined,
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
