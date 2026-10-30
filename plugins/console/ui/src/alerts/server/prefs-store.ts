// Prefs on the server, behind the same ConsolePrefsStore the Settings panel and tray already use.
// "Notify me about" is the one server field (notify_level); the panel's bell and toast thresholds are
// derived from it, so both stay in step. The old localStorage value is imported once, server-side.

import { levelOf, levelPatch } from '../../prefs/levels'
import { sanitizePrefs } from '../../prefs/sanitize'
import { CONSOLE_PREFS_STORAGE_KEY } from '../../prefs/store'
import { DEFAULT_CONSOLE_PREFS, type ConsolePrefs, type ConsolePrefsStore } from '../../prefs/types'
import type { AlertsClient } from './client'
import type { ServerPrefs } from './types'

export const fromServerPrefs = (p: ServerPrefs): ConsolePrefs =>
  sanitizePrefs({
    ...DEFAULT_CONSOLE_PREFS,
    ...levelPatch(p.notify_level),
    toastsEnabled: p.toasts,
    quiet: p.quiet,
    quietHoursEnabled: p.quiet_hours.enabled,
    quietHoursStart: p.quiet_hours.start,
    quietHoursEnd: p.quiet_hours.end,
    mutedSources: p.muted_sources,
    coalesceWindowSec: p.coalesce_window_sec,
  })

const deviceZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export const toServerPrefs = (p: ConsolePrefs, base?: ServerPrefs): ServerPrefs => ({
  notify_level: levelOf(p),
  toasts: p.toastsEnabled,
  quiet: p.quiet,
  quiet_hours: { enabled: p.quietHoursEnabled, start: p.quietHoursStart, end: p.quietHoursEnd, tz: base?.quiet_hours.tz || deviceZone() },
  muted_sources: p.mutedSources,
  coalesce_window_sec: p.coalesceWindowSec,
})

function legacyLocal(): Partial<ConsolePrefs> | null {
  try {
    const raw = window.localStorage.getItem(CONSOLE_PREFS_STORAGE_KEY)
    return raw ? (JSON.parse(raw) as Partial<ConsolePrefs>) : null
  } catch {
    return null
  }
}

export class ServerPrefsStore implements ConsolePrefsStore {
  private value: ConsolePrefs = DEFAULT_CONSOLE_PREFS
  private server: ServerPrefs | undefined
  private listeners = new Set<() => void>()
  private saving: Promise<unknown> = Promise.resolve()
  /** Last save failure, plain text, for the Settings panel to show. */
  error: string | null = null

  constructor(private client: AlertsClient) {}

  get = () => this.value
  subscribe = (l: () => void) => {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }
  private emit() {
    this.listeners.forEach((l) => l())
  }
  private adopt(p: ServerPrefs) {
    this.server = p
    this.value = fromServerPrefs(p)
    this.emit()
  }

  async load(): Promise<void> {
    try {
      const legacy = legacyLocal()
      if (legacy && !this.server) {
        // Idempotent: applied only while the server has no prefs yet.
        const r = await this.client.migratePrefs(legacy)
        this.adopt(r.prefs)
        return
      }
      this.adopt(await this.client.prefs())
    } catch {
      /* keep what we have; the panel stays usable and the next set retries */
    }
  }

  set(patch: Partial<ConsolePrefs>): void {
    const before = { value: this.value, server: this.server }
    this.value = sanitizePrefs({ ...this.value, ...patch })
    this.error = null
    this.emit()
    const body = toServerPrefs(this.value, this.server)
    // Saves go out in order so the last edit wins.
    this.saving = this.saving.then(async () => {
      try {
        this.server = await this.client.savePrefs(body)
        this.value = fromServerPrefs(this.server)
      } catch {
        this.value = before.value
        this.server = before.server
        this.error = 'Settings did not save. Try again.'
      }
      this.emit()
    })
  }
}
