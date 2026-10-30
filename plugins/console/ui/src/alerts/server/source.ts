// Which alerts source the shell mounts. Server alerts are opt-in until verified end to end; the
// console-event feed stays the default and the fallback.
export const ALERTS_SOURCE_KEY = 'tachyon.alerts.source'

export function serverAlertsEnabled(): boolean {
  try {
    if (typeof window !== 'undefined' && window.localStorage.getItem(ALERTS_SOURCE_KEY) === 'server') return true
  } catch {
    /* storage unavailable */
  }
  return (import.meta as { env?: Record<string, string | undefined> }).env?.VITE_ALERTS_SOURCE === 'server'
}
