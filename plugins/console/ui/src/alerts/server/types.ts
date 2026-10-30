// Wire shapes of the server alerts API (internal/alertsapi/openapi.yaml). snake_case, times in unix ms.

import type { ConsoleSeverity } from '../../events/types'
import type { AlertIconKey } from '../types'

export type ServerStatus = 'active' | 'dismissed' | 'snoozed' | 'deferred' | 'settled'

export interface ServerSubject {
  kind: string
  ref: string
  label?: string
  url?: string
}

export interface ServerAlert {
  alert_id: string
  seq: number
  kind: string
  rule_id: string
  severity: ConsoleSeverity
  severity_base: ConsoleSeverity
  needs_you: boolean
  title: string
  summary: string
  source: string
  icon: AlertIconKey
  status: ServerStatus
  read: boolean
  count: number
  coalesce_key: string
  first_at_ms: number
  last_at_ms: number
  snooze_until_ms: number | null
  wake_at_ms: number | null
  note: string
  resolution: string
  resolved_by: string
  primary: { label: string; href: string } | null
  subjects: ServerSubject[]
  escalate_at_ms: number | null
  escalated_at_ms: number | null
  version: number
  created_ms: number
  updated_ms: number
}

export interface ServerCounts {
  unread: number
  active: number
  needs_you: number
  by_severity: Record<string, number>
  seq: number
}

export interface ServerAlertList {
  alerts: ServerAlert[]
  next: string | null
  counts: ServerCounts
  seq: number
  server_now_ms: number
}

export interface ServerPrefs {
  notify_level: 'blockers' | 'warnings' | 'everything'
  toasts: boolean
  quiet: boolean
  quiet_hours: { enabled: boolean; start: string; end: string; tz: string }
  muted_sources: string[]
  coalesce_window_sec: number
}

export interface ServerSource {
  source: string
  state: 'ok' | 'degraded' | 'down'
  last_event_ms: number
  note: string
}

export type ServerOp =
  | { op: 'read' | 'unread' | 'dismiss' | 'reopen' }
  | { op: 'snooze'; preset?: '15m' | '1h' | '4h' | 'tomorrow'; until_ms?: number }
  | { op: 'defer'; until_ms?: number; until_ref_change?: { kind: string; ref: string } }
  | { op: 'settle'; resolution?: string; note?: string }

export interface ServerFrame {
  type: 'alert.created' | 'alert.updated' | 'alert.state' | 'alert.woke' | 'alert.escalated' | 'alert.cleared' | 'alert.removed' | 'counts' | 'prefs' | 'resync'
  at_ms: number
  alert?: ServerAlert
  alert_id?: string
  toast?: boolean
  reason?: string
  counts?: ServerCounts
  prefs?: ServerPrefs
}
