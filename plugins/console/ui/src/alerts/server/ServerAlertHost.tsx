import { useCallback, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from 'react'
import { AlertsContext, type AlertsController } from '../context'
import { AlertToastStack } from '../AlertToastStack'
import { AlertTray } from '../AlertTray'
import { ServerAlertDetailModal } from './ServerAlertDetail'
import { ToastQueue } from '../toast-queue'
import { severityAtLeast } from '../policy'
import { summarize, useNow } from '../use-alerts'
import { toAlert, toEntry } from './adapt'
import { createAlertsClient, type AlertsClient } from './client'
import { ServerPrefsStore } from './prefs-store'
import { ServerAlertsStore } from './store'
import type { ServerAlert } from './types'

export interface ServerAlertHostProps {
  children?: ReactNode
  client?: AlertsClient
}

const PRESET_MS = { '15m': 15 * 60_000, '1h': 3_600_000, '4h': 4 * 3_600_000 } as const

/**
 * The alerts surface over the server's alerts API: list and counts on load, the SSE stream afterwards,
 * ops with expected_version, prefs on the server. Provides the same controller as <AlertHost>, so the
 * tray, toasts and badge are unchanged. Toasting is the server's call (the frame's `toast` flag).
 */
export function ServerAlertHost({ children, client: clientProp }: ServerAlertHostProps) {
  const [client] = useState(() => clientProp ?? createAlertsClient())
  const [store] = useState(() => new ServerAlertsStore(client))
  const [prefsStore] = useState(() => new ServerPrefsStore(client))
  const [queue] = useState(() => new ToastQueue())
  const [open, setOpen] = useState(false)
  const [expandedId, expand] = useState<string | null>(null)

  const snap = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot)
  const prefs = useSyncExternalStore(prefsStore.subscribe, prefsStore.get, prefsStore.get)
  const now = useNow() + snap.skewMs

  useEffect(() => store.start(), [store])
  useEffect(() => {
    void prefsStore.load()
    return store.onPrefsChanged(() => void prefsStore.load())
  }, [store, prefsStore])
  useEffect(() => store.onToast((a: ServerAlert) => queue.push(toAlert(a))), [store, queue])

  const entries = useMemo(() => snap.alerts.map(toEntry), [snap.alerts])
  const summary = useMemo(() => summarize(entries.filter((e) => severityAtLeast(e.alert.severity, prefs.bellMinSeverity))), [entries, prefs.bellMinSeverity])
  useEffect(() => {
    queue.retain(new Set(entries.filter((e) => e.status === 'unread').map((e) => e.alert.id)))
  }, [queue, entries])

  const op = useCallback(
    (id: string, o: Parameters<ServerAlertsStore['op']>[1], optimistic: (a: ServerAlert) => ServerAlert) => void store.op(id, o, optimistic),
    [store],
  )
  const controller = useMemo<AlertsController>(
    () => ({
      ...summary,
      entries,
      now,
      open,
      setOpen,
      prefs,
      setPrefs: (p) => prefsStore.set(p),
      queue,
      feed: { ready: snap.ready, status: snap.connection === 'down' ? 'error' : snap.connection, sourcesDown: snap.sourcesDown, store, retry: () => void store.retry() },
      error: snap.error ?? prefsStore.error,
      markRead: (id) => op(id, { op: 'read' }, (a) => ({ ...a, read: true })),
      markAllRead: () => {
        const ids = summary.active.filter((e) => e.status === 'unread').map((e) => e.alert.id).slice(0, 200)
        if (ids.length) void store.bulk('read', ids, (a) => ({ ...a, read: true }))
      },
      dismiss: (id) => op(id, { op: 'dismiss' }, (a) => ({ ...a, status: 'dismissed' })),
      snooze: (id, ms) => {
        const until = Date.now() + snap.skewMs + ms
        const preset = (Object.keys(PRESET_MS) as (keyof typeof PRESET_MS)[]).find((k) => PRESET_MS[k] === ms)
        op(id, preset ? { op: 'snooze', preset } : { op: 'snooze', until_ms: until }, (a) => ({ ...a, status: 'snoozed', snooze_until_ms: until }))
      },
      settle: (id, resolution, note) => op(id, { op: 'settle', ...(resolution ? { resolution } : {}), ...(note ? { note } : {}) }, (a) => ({ ...a, status: 'settled', resolution: resolution ?? 'Handled' })),
      deferUntilChange: (id, ref) => op(id, { op: 'defer', until_ref_change: ref }, (a) => ({ ...a, status: 'deferred' })),
      restore: (id) => op(id, { op: 'reopen' }, (a) => ({ ...a, status: 'active', snooze_until_ms: null, wake_at_ms: null, resolution: '' })),
      expandedId,
      expand,
    }),
    [summary, entries, now, open, prefs, prefsStore, queue, snap.ready, snap.connection, snap.sourcesDown, snap.error, snap.skewMs, op, store, expandedId],
  )

  return (
    <AlertsContext.Provider value={controller}>
      {children}
      <AlertToastStack />
      <AlertTray />
      <ServerAlertDetailModal client={client} />
    </AlertsContext.Provider>
  )
}
